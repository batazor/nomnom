import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";

import { MongoClient } from "mongodb";

import { createConsumerContainer } from "../di/consumer.ts";
import { createMigrateContainer } from "../di/migrate.ts";
import { createRelayContainer } from "../di/relay.ts";
import {
  createRestaurantsContainer,
  type RestaurantsResources,
} from "../di/restaurants.ts";
import { RejectingSubjectResolver } from "../infrastructure/grpc/subject.ts";
import { type AppConfig, loadConfig } from "../pkg/config/config.ts";
import {
  connectMongoDb,
  type MongoDbConnection,
} from "../pkg/infrastructure/mongodb/client.ts";
import { Restaurant } from "../slices/restaurant/domains/restaurant/restaurant.ts";

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
    subjects: new RejectingSubjectResolver(),
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
    assert.equal(cradle.kitchenAccessControl, cradle.spicedbAccessControl);
    assert.equal(cradle.restaurantAccessControl, cradle.spicedbAccessControl);
    assert.equal(cradle.ownerRelationships, cradle.relationshipWriter);
    assert.equal(cradle.staffRelationships, cradle.relationshipWriter);
    // One Unit of Work joins repositories and the Outbox in a transaction.
    assert.equal(container.resolve("unitOfWork"), cradle.unitOfWork);
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

test("the relay and migrate containers resolve every registration", async () => {
  const mongo = offlineMongo();
  const relay = createRelayContainer({
    outbox: loadConfig({}).outbox,
    db: mongo.db,
    publisher: { publish: async () => {} },
    log: () => {},
  });
  const migrate = createMigrateContainer({ db: mongo.db, log: () => {} });
  try {
    for (const container of [relay, migrate]) {
      for (const name of Object.keys(container.registrations)) {
        assert.notEqual(container.resolve(name), undefined, name);
      }
    }
  } finally {
    await relay.dispose();
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
    log: () => {},
  });
  try {
    for (const name of Object.keys(container.registrations)) {
      assert.notEqual(container.resolve(name), undefined, name);
    }
    const { cradle } = container;
    assert.equal(cradle.worker, cradle.worker, "worker is a singleton");
    // Receive Order Request is authorized only for the orders event stream.
    assert.equal(
      await cradle.kitchenAccessControl.canCoordinateFulfillment(
        { type: "service", id: "orders" },
        "any",
      ),
      true,
    );
    assert.equal(
      await cradle.kitchenAccessControl.canOperateKitchen(
        { type: "user", id: "cook" },
        "any",
      ),
      false,
    );
  } finally {
    await container.dispose();
    await mongo.close();
  }
});

// Runs against a MongoDB replica set, see test/mongodb-integration.test.ts.
const uri = process.env.MONGODB_URI?.trim() ?? "";
const skip = uri === "" ? "MONGODB_URI is not set" : false;

describe("composed processes against MongoDB", { skip }, () => {
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
    const migrate = createMigrateContainer({ db: mongo.db, log: () => {} });
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
