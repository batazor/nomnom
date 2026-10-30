import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";

import { MongoClient } from "mongodb";

import { createConsumerContainer } from "../di/consumer.ts";
import {
  createRestaurantsContainer,
  type RestaurantsResources,
} from "../di/restaurants.ts";
import {
  connectDatabase,
  type DatabaseConnection,
} from "../infrastructure/repository/client.ts";
import { migrateRestaurantsDatabase } from "../infrastructure/repository/migrate.ts";
import {
  type AppConfig,
  loadConfig,
  loadScheduledSlotConfig,
  loadSchemaRegistryConfig,
  loadSimulatorConfig,
  loadSpiceDbConfig,
} from "../pkg/config/config.ts";
import { silentLogger } from "../pkg/telemetry/logger.ts";
import { Restaurant } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import { databaseUri } from "./support/database.ts";
import { rejectingSubjects } from "./support/grpc.ts";
import { offlineValkey, testValkeyConfig } from "./support/valkey.ts";

const simulatorEnvironment = {
  SIMULATOR__INTERVAL: "5s",
  SIMULATOR__MIN_DELAY: "30s",
  SIMULATOR__MAX_DELAY: "2m",
};

/** SCHEDULED_SLOT__* of .env.example: required in cmd/restaurants. */
const scheduledSlotEnvironment = {
  SCHEDULED_SLOT__WIDTH: "15m",
  SCHEDULED_SLOT__MIN_LEAD: "45m",
  SCHEDULED_SLOT__MAX_HORIZON: "48h",
  SCHEDULED_SLOT__HOLD_TTL: "10m",
  SCHEDULED_SLOT__ALTERNATIVES: "3",
};

const environment = {
  SPICEDB__ENDPOINT: "127.0.0.1:1",
  SPICEDB__PRESHARED_KEY: "test",
  SPICEDB__INSECURE: "true",
};

/** A client that is never connected: building the graph opens no socket. */
const offlineDatabase = (): DatabaseConnection => {
  const client = new MongoClient("mongodb://127.0.0.1:1/");
  return { client, db: client.db("restaurants"), close: () => client.close() };
};

const resourcesOf = (
  config: AppConfig,
  database: DatabaseConnection,
): RestaurantsResources => {
  return {
    server: config.server,
    spicedb: loadSpiceDbConfig(environment),
    reconciler: config.reconciler,
    kitchen: config.kitchen,
    waitTime: config.waitTime,
    simulator: loadSimulatorConfig(simulatorEnvironment),
    scheduledSlot: loadScheduledSlotConfig(scheduledSlotEnvironment),
    database,
    valkeyConfig: testValkeyConfig("redis://127.0.0.1:1"),
    valkey: offlineValkey(),
    subjects: rejectingSubjects,
    logger: silentLogger,
  };
};

test("the restaurants container resolves every registration in the application scope", async () => {
  const database = offlineDatabase();
  const container = createRestaurantsContainer(
    resourcesOf(loadConfig(environment), database),
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
    await database.close();
  }
});

test("the kitchen and wait time tunables reach the composed policy from configuration", async () => {
  const database = offlineDatabase();
  const policyOf = async (config: AppConfig) => {
    const container = createRestaurantsContainer(resourcesOf(config, database));
    try {
      return container.cradle.waitTimePolicy;
    } finally {
      await container.dispose();
    }
  };
  try {
    const defaults = await policyOf(loadConfig(environment));
    assert.deepEqual(
      {
        sampleSize: defaults.sampleSize,
        recentWindowMs: defaults.recentWindowMs,
        elevatedFactor: defaults.elevatedFactor,
        normalFactor: defaults.normalFactor,
        preparationTimeMs: defaults.preparationTimeMs,
      },
      {
        sampleSize: 50,
        recentWindowMs: 30 * 60_000,
        elevatedFactor: 1.5,
        normalFactor: 1.2,
        preparationTimeMs: 20 * 60_000,
      },
    );
    const tuned = await policyOf(
      loadConfig({
        ...environment,
        KITCHEN__PREPARATION_TIME: "35m",
        WAIT_TIME__SAMPLE_SIZE: "10",
        WAIT_TIME__ELEVATED_FACTOR: "2",
      }),
    );
    assert.equal(tuned.preparationTimeMs, 35 * 60_000);
    assert.equal(tuned.sampleSize, 10);
    assert.equal(tuned.elevatedFactor, 2);
  } finally {
    await database.close();
  }
});

test("the consumer container resolves every registration", async () => {
  const database = offlineDatabase();
  const container = createConsumerContainer({
    consumer: loadConfig({}).consumer,
    kitchen: loadConfig({}).kitchen,
    waitTime: loadConfig({}).waitTime,
    database,
    kafkaConsumer: {
      connect: async () => {},
      subscribe: async () => {},
      run: async () => {},
      disconnect: async () => {},
    },
    cacheRefreshConsumer: {
      connect: async () => {},
      subscribe: async () => {},
      run: async () => {},
      disconnect: async () => {},
    },
    valkeyConfig: testValkeyConfig("redis://127.0.0.1:1"),
    valkey: offlineValkey(),
    deadLetters: { publish: async () => {} },
    schemaRegistry: loadSchemaRegistryConfig({
      SCHEMA_REGISTRY__URL: "http://127.0.0.1:1",
    }),
    logger: silentLogger,
  });
  try {
    for (const name of Object.keys(container.registrations)) {
      assert.notEqual(container.resolve(name), undefined, name);
    }
    const { cradle } = container;
    assert.equal(cradle.worker, cradle.worker, "worker is a singleton");
    // The consumer writes no Restaurant Orders: the workflow of orders creates
    // them and reports the Pickup by RPC.
    assert.equal("recordPickup" in cradle, false);
  } finally {
    await container.dispose();
    await database.close();
  }
});

// Runs against the MongoDB testcontainer of the global setup.
const uri = databaseUri();

describe("composed processes against MongoDB", () => {
  let database: DatabaseConnection;

  before(async () => {
    database = await connectDatabase({
      uri,
      database: `restaurants_test_${randomUUID().slice(0, 8)}`,
      timeoutMs: 5000,
    });
  });

  after(async () => {
    if (database === undefined) return;
    await database.db.dropDatabase();
    await database.close();
  });

  test("startup migration prepares the database the restaurants graph reads", async () => {
    await migrateRestaurantsDatabase(database.db);

    const container = createRestaurantsContainer(
      resourcesOf(loadConfig(environment), database),
    );
    try {
      const restaurant = Restaurant.register({
        id: "r-composed",
        name: "Pho House",
        ownerId: "owner",
        marketId: "berlin",
        timeZone: "Europe/Berlin",
        registeredAt: new Date(Date.UTC(2026, 9, 25, 9)),
      });
      await container.cradle.restaurants.save(restaurant);
      const view = await container.cradle.getRestaurant.execute({
        restaurantId: "r-composed",
      });
      assert.equal(view.name, "Pho House");
      assert.equal(view.marketId, "berlin");
    } finally {
      await container.dispose();
    }
  });
});
