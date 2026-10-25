import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";

import { MongoClient } from "mongodb";

import { createConsumerContainer } from "../di/consumer.ts";
import { createMigrateContainer } from "../di/migrate.ts";
import {
  createRestaurantsContainer,
  type RestaurantsResources,
} from "../di/restaurants.ts";
import { type AppConfig, loadConfig } from "../pkg/config/config.ts";
import {
  connectMongoDb,
  type MongoDbConnection,
} from "../pkg/infrastructure/mongodb/client.ts";
import { silentLogger } from "../pkg/telemetry/logger.ts";
import { Restaurant } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import { rejectingSubjects } from "./support/grpc.ts";
import { mongoUri } from "./support/mongodb.ts";

const environment = {
  SPICEDB__ENDPOINT: "127.0.0.1:1",
  SPICEDB__PRESHARED_KEY: "test",
  SPICEDB__INSECURE: "true",
};

/** A client that is never connected: building the graph opens no socket. */
const offlineMongo = (): MongoDbConnection => {
  const client = new MongoClient("mongodb://127.0.0.1:1/");
  return { client, db: client.db("restaurants"), close: () => client.close() };
};

const resourcesOf = (
  config: AppConfig,
  mongo: MongoDbConnection,
): RestaurantsResources => {
  if (!config.spicedb.enabled) throw new Error("SpiceDB must be configured");
  return {
    server: config.server,
    spicedb: config.spicedb,
    reconciler: config.reconciler,
    kitchen: config.kitchen,
    mongo,
    subjects: rejectingSubjects,
    logger: silentLogger,
  };
};

test("the restaurants container resolves every registration in the application scope", async () => {
  const mongo = offlineMongo();
  const container = createRestaurantsContainer(
    resourcesOf(loadConfig(environment), mongo),
  );
  try {
    for (const name of Object.keys(container.registrations)) {
      assert.notEqual(
        container.resolve(name),
        undefined,
        `${name} is registered`,
      );
    }
    const { cradle } = container;
    assert.equal(cradle.worker, cradle.worker, "worker is a singleton");
    // Use cases are decorated by UseCaseTelemetry (di/usecases.ts).
    assert.ok(Object.hasOwn(cradle.acceptRestaurantOrder, "execute"));
    assert.ok(Object.hasOwn(cradle.getRestaurant, "execute"));
    assert.equal(cradle.getRestaurant, cradle.getRestaurant, "singleton");
    assert.equal(cradle.kitchenAccessControl, cradle.spicedbAccessControl);
    assert.equal(cradle.restaurantAccessControl, cradle.spicedbAccessControl);
    assert.equal(cradle.ownerRelationships, cradle.relationshipWriter);
    assert.equal(cradle.staffRelationships, cradle.relationshipWriter);
    // One Unit of Work joins repositories and the Outbox in a transaction.
    assert.equal(container.resolve("unitOfWork"), cradle.unitOfWork);
    // The Outbox mapper dispatches every aggregate to its own stream.
    const occurredAt = new Date(0);
    assert.deepEqual(
      [
        [
          { type: "Restaurant", id: "r-1", version: 2 },
          { type: "RestaurantOpened", restaurantId: "r-1", occurredAt },
        ],
        [
          { type: "Menu", id: "m-1", version: 3 },
          {
            type: "MenuItemAvailabilityChanged",
            menuId: "m-1",
            restaurantId: "r-1",
            menuItemId: "pho",
            isAvailable: false,
            occurredAt,
          },
        ],
        [
          { type: "RestaurantOrder", id: "ro-1", version: 2 },
          { type: "PreparationStarted", orderId: "o-1", occurredAt },
        ],
        [
          { type: "Restaurant", id: "r-1", version: 1 },
          { type: "RestaurantRegistered", restaurantId: "r-1", occurredAt },
        ],
      ].map(
        ([aggregate, event]) =>
          cradle.outboxEventMapper(
            aggregate as { type: string; id: string; version: number },
            event as { type: string; occurredAt: Date },
          )?.topic,
      ),
      [
        "restaurants.v1.restaurant-events",
        "restaurants.v1.menu-events",
        "restaurants.v1.restaurant-order-events",
        undefined,
      ],
    );
  } finally {
    await container.dispose();
    await mongo.close();
  }
});

test("the kitchen tunables reach the composed handlers from configuration", async () => {
  const mongo = offlineMongo();
  const acceptedAt = new Date(Date.UTC(2026, 9, 25, 9));
  const estimate = async (config: AppConfig) => {
    const container = createRestaurantsContainer(resourcesOf(config, mongo));
    try {
      return await container.cradle.readyTimeEstimator.estimate({
        restaurantId: "restaurant",
        items: [],
        acceptedAt,
      });
    } finally {
      await container.dispose();
    }
  };
  try {
    assert.equal(
      (await estimate(loadConfig(environment))).getTime() -
        acceptedAt.getTime(),
      20 * 60_000,
    );
    assert.equal(
      (
        await estimate(
          loadConfig({ ...environment, KITCHEN__PREPARATION_TIME: "35m" }),
        )
      ).getTime() - acceptedAt.getTime(),
      35 * 60_000,
    );
  } finally {
    await mongo.close();
  }
});

test("the migrate container resolves every registration", async () => {
  const mongo = offlineMongo();
  const migrate = createMigrateContainer({
    db: mongo.db,
    logger: silentLogger,
  });
  try {
    for (const name of Object.keys(migrate.registrations)) {
      assert.notEqual(migrate.resolve(name), undefined, name);
    }
  } finally {
    await migrate.dispose();
    await mongo.close();
  }
});

test("the consumer container resolves every registration", async () => {
  const mongo = offlineMongo();
  const container = createConsumerContainer({
    consumer: loadConfig({}).consumer,
    mongo,
    kafkaConsumer: {
      connect: async () => {},
      subscribe: async () => {},
      run: async () => {},
      disconnect: async () => {},
    },
    deadLetters: { publish: async () => {} },
    logger: silentLogger,
  });
  try {
    for (const name of Object.keys(container.registrations)) {
      assert.notEqual(container.resolve(name), undefined, name);
    }
    const { cradle } = container;
    assert.equal(cradle.worker, cradle.worker, "worker is a singleton");
    assert.ok(Object.hasOwn(cradle.receiveOrderRequest, "execute"));
    // Receive Order Request is authorized only for the orders event stream;
    // the consumer has no kitchen staff or read access at all.
    assert.equal(
      await cradle.fulfillmentAccess.canCoordinateFulfillment(
        { type: "service", id: "orders" },
        "any",
      ),
      true,
    );
    assert.equal("canOperateKitchen" in cradle.fulfillmentAccess, false);
  } finally {
    await container.dispose();
    await mongo.close();
  }
});

// Runs against the MongoDB testcontainer of the global setup.
const uri = mongoUri();

describe("composed processes against MongoDB", () => {
  let mongo: MongoDbConnection;

  before(async () => {
    mongo = await connectMongoDb({
      enabled: true,
      uri,
      database: `restaurants_test_${randomUUID().slice(0, 8)}`,
      timeoutMs: 5000,
    });
  });

  after(async () => {
    if (mongo === undefined) return;
    await mongo.db.dropDatabase();
    await mongo.close();
  });

  test("migrate prepares the database the restaurants graph reads", async () => {
    const migrate = createMigrateContainer({
      db: mongo.db,
      logger: silentLogger,
    });
    await migrate.cradle.migration.run();
    await migrate.dispose();

    const container = createRestaurantsContainer(
      resourcesOf(loadConfig(environment), mongo),
    );
    try {
      const restaurant = Restaurant.register({
        id: "r-composed",
        name: "Pho House",
        ownerId: "owner",
        address: "1 Pho Street",
        registeredAt: new Date(Date.UTC(2026, 9, 25, 9)),
      });
      await container.cradle.restaurants.save(restaurant);
      const view = await container.cradle.getRestaurant.execute({
        restaurantId: "r-composed",
      });
      assert.equal(view.name, "Pho House");
    } finally {
      await container.dispose();
    }
  });
});
