import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdtemp, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, describe, test } from "node:test";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";

import { type Db, MongoServerError } from "mongodb";
import {
  migrateRestaurantsDatabase,
  RESTAURANTS_MIGRATION_SETS,
} from "../infrastructure/repository/migrate.ts";
import {
  AlreadyExistsError,
  ConcurrencyConflictError,
} from "../pkg/application/errors.ts";
import {
  connectDatabase,
  type DatabaseConnection,
} from "../pkg/infrastructure/repository/client.ts";
import {
  MIGRATIONS_LOCK_COLLECTION,
  type MigrationSet,
  migrate,
  migrationsChangelogCollection,
} from "../pkg/infrastructure/repository/migrate.ts";
import { up as dropOutboxRelay } from "../pkg/infrastructure/repository/migrations/0003-outbox-drop-polling-relay.ts";
import {
  DatabaseOutbox,
  OUTBOX_COLLECTION,
  type OutboxEventMapper,
  outboxEventId,
} from "../pkg/infrastructure/repository/outbox.ts";
import { DatabaseUnitOfWork } from "../pkg/infrastructure/repository/unit-of-work.ts";
import { Menu } from "../slices/menu/domains/menu/menu.ts";
import { Money } from "../slices/menu/domains/menu/vo/money.ts";
import {
  createMenuCollection,
  DatabaseMenuReader,
  DatabaseMenuRepository,
} from "../slices/menu/infrastructure/repository/menus.ts";
import { up as menusMarket } from "../slices/menu/infrastructure/repository/migrations/0002-market.ts";
import { Restaurant } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import { up as restaurantsMarket } from "../slices/restaurant/infrastructure/repository/migrations/0003-market.ts";
import {
  createRestaurantCollection,
  DatabaseRestaurantCatalog,
  DatabaseRestaurantMemberships,
  DatabaseRestaurantRepository,
} from "../slices/restaurant/infrastructure/repository/restaurants.ts";
import {
  DuplicateOrderIdError,
  RestaurantOrder,
  RestaurantOrderState,
} from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import { up as restaurantOrdersMarket } from "../slices/restaurantorder/infrastructure/repository/migrations/0003-market.ts";
import {
  createRestaurantOrderCollection,
  DatabaseKitchenBoard,
  DatabaseRestaurantOrderRepository,
} from "../slices/restaurantorder/infrastructure/repository/restaurant-orders.ts";
import { databaseUri, noKitchenChanges } from "./support/database.ts";
import { testWaitTimePolicy } from "./support/restaurants.ts";

// Runs against a MongoDB replica set (transactions), for example
// the testcontainer of the global setup (test/support/global-containers.ts).
const uri = databaseUri();

const now = new Date(Date.UTC(2026, 9, 25, 9));

const testMapper: OutboxEventMapper = (_aggregate, event) => ({
  eventType: `nomnom.restaurants.v1.${event.type}`,
  topic: "restaurants.test",
  payload: new TextEncoder().encode(event.type),
});

describe("startup migration", () => {
  let database: DatabaseConnection;
  /** File names of every set, in the order of RESTAURANTS_MIGRATION_SETS. */
  let files: Map<string, string[]>;
  /** Every migration as `<id>/<fileName>`, in application order. */
  let migrations: string[];

  before(async () => {
    database = await connectDatabase({
      enabled: true,
      uri,
      database: "restaurants_test",
      timeoutMs: 5000,
    });
    files = new Map();
    for (const set of RESTAURANTS_MIGRATION_SETS) {
      files.set(
        set.id,
        (await readdir(set.dir)).filter((name) => name.endsWith(".ts")).sort(),
      );
    }
    migrations = [...files].flatMap(([set, names]) =>
      names.map((name) => `${set}/${name}`),
    );
  });

  after(async () => {
    await database?.close();
  });

  const freshDatabase = (t: { after(fn: () => Promise<void>): void }) => {
    const db = database.client.db(
      `restaurants_test_${randomUUID().slice(0, 8)}`,
    );
    t.after(async () => {
      await db.dropDatabase();
    });
    return db;
  };

  const collectionNames = async (db: Db) =>
    (await db.listCollections({}, { nameOnly: true }).toArray()).map(
      (collection) => collection.name,
    );

  const changelogOf = async (db: Db, set: MigrationSet) =>
    (
      await db
        .collection<{ fileName: string }>(migrationsChangelogCollection(set))
        .find()
        .toArray()
    ).map((entry) => entry.fileName);

  test("applies every set in order on an empty database and nothing on a migrated one", async (t) => {
    const db = freshDatabase(t);

    assert.deepEqual(await migrateRestaurantsDatabase(db), migrations);
    const collections = await collectionNames(db);
    for (const name of [
      "restaurants",
      "menus",
      "restaurant_orders",
      "simulator",
      OUTBOX_COLLECTION,
      "inbox",
    ]) {
      assert.ok(collections.includes(name), name);
    }
    // One changelog per set, named by its storage id only: part of the
    // schema contract, independent of directory and slice names.
    for (const name of [
      "migrations_changelog_messaging",
      "migrations_changelog_restaurants",
      "migrations_changelog_menus",
      "migrations_changelog_restaurant_orders",
      "migrations_changelog_simulator",
    ]) {
      assert.ok(collections.includes(name), name);
    }
    for (const set of RESTAURANTS_MIGRATION_SETS) {
      assert.deepEqual(await changelogOf(db, set), files.get(set.id));
    }

    const messages: string[] = [];
    assert.deepEqual(
      await migrateRestaurantsDatabase(db, (message) => messages.push(message)),
      [],
    );
    assert.deepEqual(messages, [
      "database schema is up to date, nothing to migrate",
    ]);
    // The lock is released after every run.
    assert.equal(
      await db.collection(MIGRATIONS_LOCK_COLLECTION).countDocuments(),
      0,
    );
  });

  test("adopts a database created by the previous migrator", async (t) => {
    const db = freshDatabase(t);
    await db.createCollection("menus", { validator: {} });
    await db
      .collection("menus")
      .createIndex(
        { restaurantId: 1 },
        { name: "menus_restaurant_unique", unique: true },
      );
    await db
      .collection("schema_migrations")
      .insertOne({ _id: "0001-outbox-cdc-drop-relay" as never });
    await db.createCollection("schema_migrations_lock");
    // The single changelog of migrate-mongo before the per-set changelogs.
    await db
      .collection("migrations_changelog")
      .insertOne({ fileName: "0001-restaurant-restaurants.ts" });

    assert.deepEqual(await migrateRestaurantsDatabase(db), migrations);
    const [menus] = await db
      .listCollections({ name: "menus" }, { nameOnly: false })
      .toArray();
    assert.equal(menus?.options?.validationLevel, "strict");
    await assert.rejects(
      db.collection("menus").insertOne({ restaurantId: 42 }),
      (error) => error instanceof MongoServerError && error.code === 121,
    );
    const collections = await collectionNames(db);
    assert.equal(collections.includes("schema_migrations"), false);
    assert.equal(collections.includes("schema_migrations_lock"), false);
    assert.equal(collections.includes("migrations_changelog"), false);
  });

  test("processes starting together are serialized by the lock", async (t) => {
    const db = freshDatabase(t);
    // Separate processes: migrate-mongo's configuration is process-wide, so
    // runs within one process are queued and never meet at the lock.
    const script = `
      import { connectDatabase } from ${JSON.stringify(import.meta.resolve("../pkg/infrastructure/repository/client.ts"))};
      import { migrateRestaurantsDatabase } from ${JSON.stringify(import.meta.resolve("../infrastructure/repository/migrate.ts"))};
      const database = await connectDatabase({
        enabled: true,
        uri: process.env.MONGODB_URI,
        database: process.env.MIGRATE_DATABASE,
        timeoutMs: 10000,
      });
      // Exits without closing the client: migrate-mongo creates the lock's
      // TTL index without awaiting it, so a close right after a run rejects.
      const applied = await migrateRestaurantsDatabase(database.db);
      process.stdout.write(JSON.stringify(applied), () => process.exit(0));
    `;
    const runs = await Promise.all(
      Array.from({ length: 4 }, async () => {
        const { stdout } = await promisify(execFile)(
          process.execPath,
          ["--input-type=module", "--eval", script],
          {
            env: { ...process.env, MIGRATE_DATABASE: db.databaseName },
            timeout: 60_000,
          },
        );
        return JSON.parse(stdout) as string[];
      }),
    );
    // Every migration is applied by some process, each set under the lock;
    // a process that read a changelog before the holder finished repeats it.
    assert.deepEqual([...new Set(runs.flat())].sort(), [...migrations].sort());
    for (const set of RESTAURANTS_MIGRATION_SETS) {
      assert.deepEqual(
        [...new Set(await changelogOf(db, set))].sort(),
        files.get(set.id),
      );
    }
    assert.equal(
      await db.collection(MIGRATIONS_LOCK_COLLECTION).countDocuments(),
      0,
    );
  });

  test("ADR 41 documents stored before Markets belong to berlin and Market lists are indexed", async (t) => {
    const db = freshDatabase(t);
    await migrateRestaurantsDatabase(db);
    // Documents of the schema before ADR 41, written past the validator.
    const legacy = { bypassDocumentValidation: true } as const;
    await db.collection<{ _id: string }>("restaurants").insertOne(
      {
        _id: "r-legacy",
        version: 1,
        schemaVersion: 1,
        name: "Pho",
        ownerId: "owner",
        isOpen: true,
      } as never,
      legacy,
    );
    await db.collection<{ _id: string }>("restaurants").insertOne(
      {
        _id: "r-hamburg",
        version: 1,
        schemaVersion: 2,
        name: "Fischbude",
        ownerId: "owner",
        marketId: "hamburg",
        isOpen: true,
      } as never,
      legacy,
    );
    await db.collection<{ _id: string }>("menus").insertOne(
      {
        _id: "m-legacy",
        version: 1,
        schemaVersion: 1,
        restaurantId: "r-legacy",
        items: [],
      } as never,
      legacy,
    );
    await db.collection<{ _id: string }>("restaurant_orders").insertOne(
      {
        _id: "ro-legacy",
        version: 1,
        schemaVersion: 2,
        orderId: "o-legacy",
        restaurantId: "r-legacy",
        items: [{ menuItemId: "pho", quantity: 1, options: [] }],
        state: "RECEIVED",
        timeline: { receivedAt: now },
      } as never,
      legacy,
    );

    // Idempotent: the startup run applied them already, a repeat migrates.
    for (let run = 0; run < 2; run += 1) {
      await restaurantsMarket(db);
      await menusMarket(db);
      await restaurantOrdersMarket(db);
    }

    const marketOf = async (collection: string, id: string) =>
      (
        await db
          .collection<{ _id: string; marketId?: string }>(collection)
          .findOne({ _id: id })
      )?.marketId;
    assert.equal(await marketOf("restaurants", "r-legacy"), "berlin");
    assert.equal(await marketOf("restaurants", "r-hamburg"), "hamburg");
    assert.equal(await marketOf("menus", "m-legacy"), "berlin");
    assert.equal(await marketOf("restaurant_orders", "ro-legacy"), "berlin");
    // The migrated documents load as aggregates of the launch Market.
    const restaurants = new DatabaseRestaurantRepository(
      createRestaurantCollection(db, new DatabaseUnitOfWork(database.client)),
      noKitchenChanges,
    );
    assert.equal((await restaurants.get("r-legacy"))?.marketId.value, "berlin");
    // The Market is required and a slug from now on.
    for (const collection of ["restaurants", "menus", "restaurant_orders"]) {
      for (const marketId of [undefined, "Berlin"]) {
        await assert.rejects(
          db
            .collection<{ _id: string }>(collection)
            .updateOne(
              { _id: { $exists: true } },
              marketId === undefined
                ? { $unset: { marketId: "" } }
                : { $set: { marketId } },
            ),
          (error) => error instanceof MongoServerError && error.code === 121,
          `${collection} ${marketId}`,
        );
      }
    }
    const indexes = (await db.collection("restaurants").indexes()).map(
      (index) => index.name,
    );
    for (const name of [
      "restaurants_market_open_by_id",
      "restaurants_market_by_id",
      "restaurants_owner_market_by_id",
    ]) {
      assert.ok(indexes.includes(name), name);
    }
    for (const name of ["restaurants_open_by_id", "restaurants_owner"]) {
      assert.equal(indexes.includes(name), false, name);
    }
  });

  test("runs of one process are queued", async (t) => {
    const db = freshDatabase(t);
    const runs = await Promise.all(
      Array.from({ length: 3 }, () => migrateRestaurantsDatabase(db)),
    );
    assert.deepEqual(runs, [migrations, [], []]);
  });

  test("the lock of a crashed process expires", async (t) => {
    // The TTL monitor removes expired documents every second instead of 60.
    const admin = database.client.db("admin");
    const { was } = await admin.command({
      setParameter: 1,
      ttlMonitorSleepSecs: 1,
    });
    t.after(async () => {
      await admin.command({ setParameter: 1, ttlMonitorSleepSecs: was });
    });
    const db = freshDatabase(t);
    await migrateRestaurantsDatabase(db);
    await db
      .collection(MIGRATIONS_LOCK_COLLECTION)
      .insertOne({ createdAt: new Date(Date.now() - 60_000) });

    const messages: string[] = [];
    assert.deepEqual(
      await migrateRestaurantsDatabase(db, (message) => messages.push(message)),
      [],
    );
    assert.equal(
      messages[0],
      "waiting for another process to finish migrating",
    );
  });

  test("a failed migration rejects and releases the lock", async (t) => {
    const dir = await mkdtemp(join(tmpdir(), "restaurants-migrations-"));
    t.after(() => rm(dir, { recursive: true }));
    await writeFile(
      join(dir, "0001-fails.ts"),
      'export async function up() { throw new Error("boom"); }\n',
    );
    const db = freshDatabase(t);
    const failing: MigrationSet = {
      id: "failing",
      dir: pathToFileURL(`${dir}/`),
    };
    const [messaging] = RESTAURANTS_MIGRATION_SETS;
    assert.ok(messaging);

    // The sets after the failed one are not applied.
    await assert.rejects(
      migrate(db, [failing, messaging]),
      /Could not migrate up 0001-fails.ts: boom/,
    );
    assert.equal(
      await db.collection(MIGRATIONS_LOCK_COLLECTION).countDocuments(),
      0,
    );
    assert.deepEqual(await changelogOf(db, failing), []);
    assert.deepEqual(await changelogOf(db, messaging), []);
  });
});

describe("MongoDB storage", () => {
  let database: DatabaseConnection;
  let unitOfWork: DatabaseUnitOfWork;

  before(async () => {
    database = await connectDatabase({
      enabled: true,
      uri,
      database: `restaurants_test_${randomUUID().slice(0, 8)}`,
      timeoutMs: 5000,
    });
    unitOfWork = new DatabaseUnitOfWork(database.client);
    await migrateRestaurantsDatabase(database.db);
  });

  after(async () => {
    if (database === undefined) return;
    await database.db.dropDatabase();
    await database.close();
  });

  const restaurantsOf = () => {
    const collection = createRestaurantCollection(database.db, unitOfWork);
    return {
      repository: new DatabaseRestaurantRepository(
        collection,
        noKitchenChanges,
      ),
      catalog: new DatabaseRestaurantCatalog(collection, {
        policy: testWaitTimePolicy(),
        now: () => new Date(),
      }),
      memberships: new DatabaseRestaurantMemberships(collection),
    };
  };

  const ordersOf = () => {
    const collection = createRestaurantOrderCollection(database.db, unitOfWork);
    return {
      repository: new DatabaseRestaurantOrderRepository(
        collection,
        noKitchenChanges,
      ),
      board: new DatabaseKitchenBoard(collection),
    };
  };

  const receive = (orderId = randomUUID(), restaurantId = "r-board") =>
    RestaurantOrder.receive({
      id: randomUUID(),
      orderId,
      restaurantId,
      marketId: "berlin",
      items: [{ menuItemId: "pho", quantity: 2, options: ["extra-herbs"] }],
      receivedAt: now,
    });

  test("migration is idempotent and creates indexes", async () => {
    await migrateRestaurantsDatabase(database.db);
    const names = (await database.db.collection("menus").indexes()).map(
      (index) => index.name,
    );
    assert.ok(names.includes("menus_restaurant_unique"));
    const collections = (await database.db.listCollections().toArray()).map(
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
    const outboxIndexes = await database.db
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
    const outbox = database.db.collection(OUTBOX_COLLECTION);
    await database.db.createCollection("outbox_counters");
    await database.db.createCollection("outbox_leases");
    await outbox.createIndex(
      { publishedAt: 1, seq: 1 },
      { name: "outbox_pending_by_seq" },
    );
    const relayRecord = randomUUID();
    await outbox.insertOne(
      { _id: relayRecord as never, seq: 1, publishedAt: null },
      { bypassDocumentValidation: true },
    );

    // Twice: a crash before the changelog entry is written runs it again.
    await dropOutboxRelay(database.db);
    await dropOutboxRelay(database.db);

    assert.equal(await outbox.countDocuments({ _id: relayRecord as never }), 0);
    assert.equal(
      (await outbox.indexes()).some(
        (index) => index.name === "outbox_pending_by_seq",
      ),
      false,
    );
    const collections = (
      await database.db.listCollections({}, { nameOnly: true }).toArray()
    ).map((collection) => collection.name);
    assert.equal(collections.includes("outbox_counters"), false);
    assert.equal(collections.includes("outbox_leases"), false);
  });

  test("validator rejects a malformed restaurant document", async () => {
    await assert.rejects(
      database.db
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
      marketId: "berlin",
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
      marketId: "berlin",
      registeredAt: now,
    });
    await assert.rejects(repository.save(duplicate), ConcurrencyConflictError);

    assert.deepEqual(
      (
        await catalog.list({
          marketId: "berlin",
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
    const collection = createMenuCollection(database.db, unitOfWork);
    const menus = new DatabaseMenuRepository(collection);
    const menu = Menu.create({
      id: "m-1",
      restaurantId: "r-1",
      marketId: "berlin",
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
    const view = await new DatabaseMenuReader(collection).getByRestaurantId(
      "r-1",
    );
    assert.equal(view?.items[0]?.price.amount, "4.50");
    assert.equal(view?.items[0]?.description, undefined);

    const second = Menu.create({
      id: "m-2",
      restaurantId: "r-1",
      marketId: "berlin",
      createdAt: now,
    });
    await assert.rejects(menus.save(second), AlreadyExistsError);
  });

  test("unit of work commits the order and its outbox records together", async () => {
    const { repository } = ordersOf();
    const outbox = new DatabaseOutbox(database.db, unitOfWork, {
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
    const records = await database.db
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
    const outbox = new DatabaseOutbox(database.db, unitOfWork);
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
    const collection = database.db.collection(OUTBOX_COLLECTION);
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
    const outbox = new DatabaseOutbox(database.db, unitOfWork);
    const order = receive();
    await unitOfWork.run(async () => {
      const version = await repository.save(order);
      await outbox.add(
        { type: "RestaurantOrder", id: order.id, version },
        order.pullEvents(),
      );
    });
    assert.equal(
      await database.db
        .collection(OUTBOX_COLLECTION)
        .countDocuments({ aggregateId: order.id }),
      0,
    );
  });

  test("unit of work rolls back the order and its outbox records", async () => {
    const { repository } = ordersOf();
    const outbox = new DatabaseOutbox(database.db, unitOfWork, {
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
      await database.db
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
