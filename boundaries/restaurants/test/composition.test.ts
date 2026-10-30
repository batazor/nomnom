import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";

import { MongoClient } from "mongodb";

import { createConsumerContainer } from "../di/consumer.ts";
import {
  createRestaurantsContainer,
  RESTAURANTS_USE_CASES,
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
import { RESTAURANT_CONSUMER_USE_CASES } from "../slices/restaurant/di/registrations.ts";
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
    // Use cases are decorated by UseCaseTelemetry (lists in the slices' di).
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

// AC-996: the instrumented use cases of both processes, by cradle key. A new
// use case adds its name here and to the list in the di of its slice.
test("processes instrument the use cases their slices list", () => {
  assert.deepEqual(RESTAURANTS_USE_CASES, {
    getRestaurant: { name: "get_restaurant", kind: "query" },
    batchGetRestaurants: { name: "batch_get_restaurants", kind: "query" },
    listRestaurants: { name: "list_restaurants", kind: "query" },
    listOwnedRestaurants: { name: "list_owned_restaurants", kind: "query" },
    registerRestaurant: { name: "register_restaurant", kind: "command" },
    updateRestaurant: { name: "update_restaurant", kind: "command" },
    setOpeningHours: { name: "set_opening_hours", kind: "command" },
    setScheduledOrderSettings: {
      name: "set_scheduled_order_settings",
      kind: "command",
    },
    assignStaff: { name: "assign_staff", kind: "command" },
    revokeStaff: { name: "revoke_staff", kind: "command" },
    openRestaurant: { name: "open_restaurant", kind: "command" },
    closeRestaurant: { name: "close_restaurant", kind: "command" },
    getMenu: { name: "get_menu", kind: "query" },
    batchGetMenus: { name: "batch_get_menus", kind: "query" },
    createMenu: { name: "create_menu", kind: "command" },
    addMenuItem: { name: "add_menu_item", kind: "command" },
    updateMenuItem: { name: "update_menu_item", kind: "command" },
    setMenuItemAvailability: {
      name: "set_menu_item_availability",
      kind: "command",
    },
    receiveOrderRequest: { name: "receive_order_request", kind: "command" },
    cancelRestaurantOrder: { name: "cancel_restaurant_order", kind: "command" },
    queueRestaurantOrder: { name: "queue_restaurant_order", kind: "command" },
    recordPickup: { name: "record_pickup", kind: "command" },
    listScheduledOrders: { name: "list_scheduled_orders", kind: "query" },
    acceptRestaurantOrder: { name: "accept_order", kind: "command" },
    rejectRestaurantOrder: { name: "reject_order", kind: "command" },
    startPreparation: { name: "start_preparation", kind: "command" },
    markRestaurantOrderAssembled: { name: "mark_assembled", kind: "command" },
    markRestaurantOrderReady: {
      name: "mark_ready_for_pickup",
      kind: "command",
    },
    reportPreparationDelay: {
      name: "report_preparation_delay",
      kind: "command",
    },
    reportPreparationFailure: {
      name: "report_preparation_failure",
      kind: "command",
    },
    listScheduledSlots: { name: "list_scheduled_slots", kind: "query" },
    reserveScheduledSlot: { name: "reserve_scheduled_slot", kind: "command" },
    confirmScheduledSlot: { name: "confirm_scheduled_slot", kind: "command" },
    releaseScheduledSlot: { name: "release_scheduled_slot", kind: "command" },
    hireCook: { name: "hire_cook", kind: "command" },
    putCookOnLine: { name: "put_cook_on_line", kind: "command" },
    takeCookOffLine: { name: "take_cook_off_line", kind: "command" },
    retireCook: { name: "retire_cook", kind: "command" },
    getCookPool: { name: "get_cook_pool", kind: "query" },
    getSimulator: { name: "get_simulator", kind: "query" },
    setSimulatorEnabled: { name: "set_simulator_enabled", kind: "command" },
  });
  assert.deepEqual(RESTAURANT_CONSUMER_USE_CASES, {
    recordWaitTime: { name: "record_wait_time", kind: "command" },
  });
});
