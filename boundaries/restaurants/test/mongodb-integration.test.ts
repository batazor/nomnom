import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";

import { MongoServerError } from "mongodb";

import {
  COLLECTIONS,
  migrateRestaurantsDatabase,
} from "../infrastructure/mongodb/collections.ts";
import {
  AlreadyExistsError,
  ConcurrencyConflictError,
} from "../pkg/application/errors.ts";
import {
  connectMongoDb,
  type MongoDbConnection,
} from "../pkg/infrastructure/mongodb/client.ts";
import {
  MIGRATION_LOCK_COLLECTION,
  MIGRATIONS_COLLECTION,
} from "../pkg/infrastructure/mongodb/migrate.ts";
import {
  dropOutboxRelayMigration,
  MongoOutbox,
  OUTBOX_COLLECTION,
  type OutboxEventMapper,
  outboxEventId,
} from "../pkg/infrastructure/mongodb/outbox.ts";
import { MongoUnitOfWork } from "../pkg/infrastructure/mongodb/unit-of-work.ts";
import { Menu } from "../slices/menu/domains/menu/menu.ts";
import { Money } from "../slices/menu/domains/menu/vo/money.ts";
import {
  createMenuCollection,
  MongoMenuReader,
  MongoMenuRepository,
} from "../slices/menu/infrastructure/mongodb/menus.ts";
import { Restaurant } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import {
  createRestaurantCollection,
  MongoRestaurantCatalog,
  MongoRestaurantMemberships,
  MongoRestaurantRepository,
} from "../slices/restaurant/infrastructure/mongodb/restaurants.ts";
import {
  DuplicateOrderIdError,
  RestaurantOrder,
  RestaurantOrderState,
} from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import {
  createRestaurantOrderCollection,
  MongoKitchenBoard,
  MongoRestaurantOrderRepository,
} from "../slices/restaurantorder/infrastructure/mongodb/restaurant-orders.ts";
import { mongoUri, noKitchenChanges } from "./support/mongodb.ts";

// Runs against a MongoDB replica set (transactions), for example
// the testcontainer of the global setup (test/support/global-containers.ts).
const uri = mongoUri();

const now = new Date(Date.UTC(2026, 9, 25, 9));

const testMapper: OutboxEventMapper = (_aggregate, event) => ({
  eventType: `nomnom.restaurants.v1.${event.type}`,
  topic: "restaurants.test",
  payload: new TextEncoder().encode(event.type),
});

describe("startup migration", () => {
  let mongo: MongoDbConnection;

  before(async () => {
    mongo = await connectMongoDb({
      enabled: true,
      uri,
      database: "restaurants_test",
      timeoutMs: 5000,
    });
  });

  after(async () => {
    await mongo?.close();
  });

  const freshDatabase = (t: { after(fn: () => Promise<void>): void }) => {
    const db = mongo.client.db(`restaurants_test_${randomUUID().slice(0, 8)}`);
    t.after(async () => {
      await db.dropDatabase();
    });
    return db;
  };

  test("applies everything on an empty database and nothing on a migrated one", async (t) => {
    const db = freshDatabase(t);
    const messages: string[] = [];

    const first = await migrateRestaurantsDatabase(db, (message) =>
      messages.push(message),
    );
    for (const spec of COLLECTIONS) {
      assert.ok(first.includes(`created collection ${spec.name}`), spec.name);
    }
    assert.ok(first.includes("created index menus.menus_restaurant_unique"));
    assert.ok(first.includes("applied migration 0001-outbox-cdc-drop-relay"));
    assert.ok(
      await db
        .collection(MIGRATIONS_COLLECTION)
        .findOne({ _id: "0001-outbox-cdc-drop-relay" as never }),
    );

    messages.length = 0;
    assert.deepEqual(
      await migrateRestaurantsDatabase(db, (message) => messages.push(message)),
      [],
    );
    assert.deepEqual(messages, [
      "database schema is up to date, nothing to migrate",
    ]);
    // The lease is released after every run.
    assert.equal(
      await db.collection(MIGRATION_LOCK_COLLECTION).countDocuments(),
      0,
    );
  });

  test("restores a changed validator and a dropped index", async (t) => {
    const db = freshDatabase(t);
    await migrateRestaurantsDatabase(db);
    await db.command({ collMod: "menus", validator: {} });
    await db.collection("menus").dropIndex("menus_restaurant_unique");

    assert.deepEqual(await migrateRestaurantsDatabase(db), [
      "updated validator of menus",
      "created index menus.menus_restaurant_unique",
    ]);
    assert.deepEqual(await migrateRestaurantsDatabase(db), []);
  });

  test("processes starting together migrate once under the lease", async (t) => {
    const db = freshDatabase(t);
    const runs = await Promise.all(
      Array.from({ length: 4 }, () => migrateRestaurantsDatabase(db)),
    );
    // One holder applies the changes; the others wait for the lease and find
    // nothing to change.
    assert.equal(runs.filter((changes) => changes.length > 0).length, 1);
    assert.equal(
      runs.flat().filter((change) => change.startsWith("applied migration"))
        .length,
      1,
    );
  });

  test("an expired lease of a crashed process does not block startup", async (t) => {
    const db = freshDatabase(t);
    await db.collection(MIGRATION_LOCK_COLLECTION).insertOne({
      _id: "migrate" as never,
      owner: "crashed",
      expiresAt: new Date(Date.now() - 1000),
    });
    assert.ok((await migrateRestaurantsDatabase(db)).length > 0);
    assert.equal(
      await db.collection(MIGRATION_LOCK_COLLECTION).countDocuments(),
      0,
    );
  });
});

describe("MongoDB storage", () => {
  let mongo: MongoDbConnection;
  let unitOfWork: MongoUnitOfWork;

  before(async () => {
    mongo = await connectMongoDb({
      enabled: true,
      uri,
      database: `restaurants_test_${randomUUID().slice(0, 8)}`,
      timeoutMs: 5000,
    });
    unitOfWork = new MongoUnitOfWork(mongo.client);
    await migrateRestaurantsDatabase(mongo.db);
  });

  after(async () => {
    if (mongo === undefined) return;
    await mongo.db.dropDatabase();
    await mongo.close();
  });

  const restaurantsOf = () => {
    const collection = createRestaurantCollection(mongo.db, unitOfWork);
    return {
      repository: new MongoRestaurantRepository(collection, noKitchenChanges),
      catalog: new MongoRestaurantCatalog(collection),
      memberships: new MongoRestaurantMemberships(collection),
    };
  };

  const ordersOf = () => {
    const collection = createRestaurantOrderCollection(mongo.db, unitOfWork);
    return {
      repository: new MongoRestaurantOrderRepository(
        collection,
        noKitchenChanges,
      ),
      board: new MongoKitchenBoard(collection),
    };
  };

  const receive = (orderId = randomUUID(), restaurantId = "r-board") =>
    RestaurantOrder.receive({
      id: randomUUID(),
      orderId,
      restaurantId,
      items: [{ menuItemId: "pho", quantity: 2, options: ["extra-herbs"] }],
      receivedAt: now,
    });

  test("migration is idempotent and creates indexes", async () => {
    await migrateRestaurantsDatabase(mongo.db);
    const names = (await mongo.db.collection("menus").indexes()).map(
      (index) => index.name,
    );
    assert.ok(names.includes("menus_restaurant_unique"));
    const collections = (await mongo.db.listCollections().toArray()).map(
      (collection) => collection.name,
    );
    for (const name of [
      "restaurants",
      "menus",
      "restaurant_orders",
      OUTBOX_COLLECTION,
    ]) {
      assert.ok(collections.includes(name), name);
    }
    const outboxIndexes = await mongo.db
      .collection(OUTBOX_COLLECTION)
      .indexes();
    assert.deepEqual(outboxIndexes.map((index) => index.name).sort(), [
      "_id_",
      "outbox_created_at_ttl",
      "outbox_event_id_unique",
    ]);
    assert.equal(
      outboxIndexes.find((index) => index.name === "outbox_created_at_ttl")
        ?.expireAfterSeconds,
      3600,
    );
  });

  test("CDC migration removes what the polling relay left behind", async () => {
    const outbox = mongo.db.collection(OUTBOX_COLLECTION);
    await mongo.db.createCollection("outbox_counters");
    await mongo.db.createCollection("outbox_leases");
    await outbox.createIndex(
      { publishedAt: 1, seq: 1 },
      { name: "outbox_pending_by_seq" },
    );
    const relayRecord = randomUUID();
    await outbox.insertOne(
      { _id: relayRecord as never, seq: 1, publishedAt: null },
      { bypassDocumentValidation: true },
    );

    // Twice: a crash before `schema_migrations` is written runs it again.
    await dropOutboxRelayMigration.up(mongo.db);
    await dropOutboxRelayMigration.up(mongo.db);

    assert.equal(await outbox.countDocuments({ _id: relayRecord as never }), 0);
    assert.equal(
      (await outbox.indexes()).some(
        (index) => index.name === "outbox_pending_by_seq",
      ),
      false,
    );
    const collections = (
      await mongo.db.listCollections({}, { nameOnly: true }).toArray()
    ).map((collection) => collection.name);
    assert.equal(collections.includes("outbox_counters"), false);
    assert.equal(collections.includes("outbox_leases"), false);
  });

  test("validator rejects a malformed restaurant document", async () => {
    await assert.rejects(
      mongo.db
        .collection<{ _id: string }>("restaurants")
        .insertOne({ _id: "bad", name: 42 } as never),
      (error) => error instanceof MongoServerError && error.code === 121,
    );
  });

  test("restaurant round trip with version and authorization revision", async () => {
    const { repository, catalog, memberships } = restaurantsOf();
    const restaurant = Restaurant.register({
      id: "r-1",
      name: "Pho House",
      ownerId: "owner",
      address: "1 Pho Street",
      registeredAt: now,
    });
    restaurant.assignStaff("cook");
    restaurant.recordAuthorizationRevision("zed-1");
    assert.equal(await repository.save(restaurant), 1);

    const loaded = await repository.get("r-1");
    assert.ok(loaded);
    assert.equal(loaded.version, 1);
    assert.deepEqual(loaded.toSnapshot(), restaurant.toSnapshot());
    assert.equal(await repository.authorizationRevision("r-1"), "zed-1");
    assert.equal(await repository.authorizationRevision("missing"), undefined);

    loaded.open(now);
    assert.equal(await repository.save(loaded), 2);
    // A stale copy loses the race.
    await assert.rejects(repository.save(restaurant), ConcurrencyConflictError);
    const duplicate = Restaurant.register({
      id: "r-1",
      name: "Copy",
      ownerId: "owner",
      address: "x",
      registeredAt: now,
    });
    await assert.rejects(repository.save(duplicate), ConcurrencyConflictError);

    assert.deepEqual(
      (
        await catalog.list({
          includeClosed: false,
          after: undefined,
          limit: 10,
        })
      ).map((view) => view.restaurantId),
      ["r-1"],
    );
    assert.deepEqual(
      await memberships.recordAuthorizationRevision("r-1", "zed-2"),
      { from: 2, to: 3 },
    );
    assert.equal(
      await memberships.recordAuthorizationRevision("missing", "zed-3"),
      undefined,
    );
    assert.equal(await repository.authorizationRevision("r-1"), "zed-2");
    assert.deepEqual(await memberships.page(undefined, 10), [
      { restaurantId: "r-1", version: 3, ownerId: "owner", staff: ["cook"] },
    ]);
  });

  test("one menu per restaurant with embedded items", async () => {
    const collection = createMenuCollection(mongo.db, unitOfWork);
    const menus = new MongoMenuRepository(collection);
    const menu = Menu.create({
      id: "m-1",
      restaurantId: "r-1",
      createdAt: now,
    });
    menu.addItem({
      id: "pho",
      name: "Pho",
      price: Money.from({ amount: "4.50", currency: "USD" }),
      isAvailable: true,
      addedAt: now,
    });
    assert.equal(await menus.save(menu), 1);
    const view = await new MongoMenuReader(collection).getByRestaurantId("r-1");
    assert.equal(view?.items[0]?.price.amount, "4.50");
    assert.equal(view?.items[0]?.description, undefined);

    const second = Menu.create({
      id: "m-2",
      restaurantId: "r-1",
      createdAt: now,
    });
    await assert.rejects(menus.save(second), AlreadyExistsError);
  });

  test("unit of work commits the order and its outbox records together", async () => {
    const { repository } = ordersOf();
    const outbox = new MongoOutbox(mongo.db, unitOfWork, {
      mapper: testMapper,
      metadata: () => ({ traceparent: "00-abc-def-01" }),
    });
    const order = receive();
    await unitOfWork.run(async () => {
      const version = await repository.save(order);
      await outbox.add(
        { type: "RestaurantOrder", id: order.id, version },
        order.pullEvents(),
      );
    });
    assert.equal((await repository.get(order.id))?.version, 1);
    const records = await mongo.db
      .collection(OUTBOX_COLLECTION)
      .find({ aggregateId: order.id })
      .toArray();
    assert.equal(records.length, 1);
    const [record] = records;
    assert.equal(
      record?.eventType,
      "nomnom.restaurants.v1.RestaurantOrderReceived",
    );
    assert.equal(
      record?.eventId,
      outboxEventId(
        { type: "RestaurantOrder", id: order.id, version: 1 },
        "nomnom.restaurants.v1.RestaurantOrderReceived",
      ),
    );
    assert.equal(record?.topic, "restaurants.test");
    assert.equal(record?.traceparent, "00-abc-def-01");
    assert.equal(
      Buffer.from(record?.payload.buffer).toString(),
      "RestaurantOrderReceived",
    );

    // Same aggregate version and type again: duplicate eventId aborts.
    await assert.rejects(
      unitOfWork.run(() =>
        outbox.add({ type: "RestaurantOrder", id: order.id, version: 1 }, [
          { type: "RestaurantOrderReceived", occurredAt: now },
        ]),
      ),
      (error) => error instanceof MongoServerError && error.code === 11000,
    );
  });

  test("outbox records carry every field of the CDC contract (ADR 37)", async () => {
    const outbox = new MongoOutbox(mongo.db, unitOfWork);
    const aggregateId = randomUUID();
    await unitOfWork.run(() =>
      outbox.notify(
        { type: "Restaurant", id: aggregateId, version: 3 },
        {
          type: "KitchenChanged",
          topic: "restaurants.test.changes",
          entity: { __typename: "Kitchen", restaurantId: aggregateId },
          occurredAt: now,
        },
      ),
    );
    const collection = mongo.db.collection(OUTBOX_COLLECTION);
    // MongoEventRouter turns these BSON types into the envelope headers; a
    // missing header field stops the connector.
    assert.equal(
      await collection.countDocuments({
        aggregateId,
        eventId: { $type: "string" },
        eventType: "KitchenChanged",
        aggregateType: "Restaurant",
        aggregateVersion: { $type: "long" },
        occurredAt: now,
        topic: "restaurants.test.changes",
        payload: { $type: "binData" },
        traceparent: "",
        createdAt: { $type: "date" },
      }),
      1,
    );
    const record = await collection.findOne({ aggregateId });
    assert.deepEqual(Object.keys(record ?? {}).sort(), [
      "_id",
      "aggregateId",
      "aggregateType",
      "aggregateVersion",
      "createdAt",
      "eventId",
      "eventType",
      "occurredAt",
      "payload",
      "schemaVersion",
      "topic",
      "traceparent",
    ]);
    assert.equal(record?.aggregateVersion, 3);
    assert.deepEqual(
      JSON.parse(Buffer.from(record?.payload.buffer).toString()),
      { __typename: "Kitchen", restaurantId: aggregateId },
    );
  });

  test("default mapper writes no outbox records", async () => {
    const { repository } = ordersOf();
    const outbox = new MongoOutbox(mongo.db, unitOfWork);
    const order = receive();
    await unitOfWork.run(async () => {
      const version = await repository.save(order);
      await outbox.add(
        { type: "RestaurantOrder", id: order.id, version },
        order.pullEvents(),
      );
    });
    assert.equal(
      await mongo.db
        .collection(OUTBOX_COLLECTION)
        .countDocuments({ aggregateId: order.id }),
      0,
    );
  });

  test("unit of work rolls back the order and its outbox records", async () => {
    const { repository } = ordersOf();
    const outbox = new MongoOutbox(mongo.db, unitOfWork, {
      mapper: testMapper,
    });
    const order = receive();
    await assert.rejects(
      unitOfWork.run(async () => {
        const version = await repository.save(order);
        await outbox.add(
          { type: "RestaurantOrder", id: order.id, version },
          order.pullEvents(),
        );
        throw new Error("boom");
      }),
      /boom/,
    );
    assert.equal(await repository.get(order.id), undefined);
    assert.equal(
      await mongo.db
        .collection(OUTBOX_COLLECTION)
        .countDocuments({ aggregateId: order.id }),
      0,
    );
  });

  test("orderId is unique and the board queries by state", async () => {
    const { repository, board } = ordersOf();
    const orderId = randomUUID();
    const first = receive(orderId, "r-unique");
    await repository.save(first);
    await assert.rejects(
      repository.save(receive(orderId, "r-unique")),
      DuplicateOrderIdError,
    );
    assert.equal((await repository.findByOrderId(orderId))?.id, first.id);

    const accepted = receive(randomUUID(), "r-queue");
    accepted.accept({
      acceptedAt: now,
      expectedReadyAt: new Date(now.getTime() + 60_000),
      eligibility: { satisfied: true, reasons: [] },
    });
    accepted.queue({ queuedAt: now });
    await repository.save(accepted);
    const queued = await board.listQueued("r-queue");
    assert.equal(queued.length, 1);
    assert.equal(queued[0]?.acceptedAt?.getTime(), now.getTime());
    assert.equal(
      (await board.get("r-queue", accepted.id))?.state,
      RestaurantOrderState.Queued,
    );
    assert.equal(await board.get("other", accepted.id), undefined);
  });
});
