import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";

import { MongoServerError } from "mongodb";

import { migrateRestaurantsDatabase } from "../infrastructure/mongodb/collections.ts";
import {
  AlreadyExistsError,
  ConcurrencyConflictError,
} from "../pkg/application/errors.ts";
import {
  connectMongoDb,
  type MongoDbConnection,
} from "../pkg/infrastructure/mongodb/client.ts";
import {
  MongoOutbox,
  MongoOutboxOutsideUnitOfWorkError,
  OUTBOX_COLLECTION,
  type OutboxEventMapper,
  outboxEventId,
  uuidV5,
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

// Runs against a MongoDB replica set (transactions), for example
// `docker run -d -p 27017:27017 mongo:8 --replSet rs0` + `rs.initiate()`:
// MONGODB_URI=mongodb://localhost:27017/?replicaSet=rs0&directConnection=true
const uri = process.env.MONGODB_URI?.trim() ?? "";
const skip = uri === "" ? "MONGODB_URI is not set" : false;

const now = new Date(Date.UTC(2026, 9, 25, 9));

const testMapper: OutboxEventMapper = (_aggregate, event) => ({
  eventType: `nomnom.restaurants.v1.${event.type}`,
  topic: "restaurants.test",
  payload: new TextEncoder().encode(event.type),
});

test("uuidV5 matches the RFC 4122 test vector", () => {
  // python: uuid.uuid5(uuid.NAMESPACE_DNS, "python.org")
  assert.equal(
    uuidV5("python.org", "6ba7b810-9dad-11d1-80b4-00c04fd430c8"),
    "886313e1-3b8a-5372-9b90-0c9aee199e5d",
  );
});

describe("MongoDB storage", { skip }, () => {
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
      repository: new MongoRestaurantRepository(collection),
      catalog: new MongoRestaurantCatalog(collection),
      memberships: new MongoRestaurantMemberships(collection),
    };
  };

  const ordersOf = () => {
    const collection = createRestaurantOrderCollection(mongo.db, unitOfWork);
    return {
      repository: new MongoRestaurantOrderRepository(collection),
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
      "outbox_counters",
      "outbox_leases",
    ]) {
      assert.ok(collections.includes(name), name);
    }
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
    await memberships.recordAuthorizationRevision("r-1", "zed-2");
    await memberships.recordAuthorizationRevision("missing", "zed-3");
    assert.equal(await repository.authorizationRevision("r-1"), "zed-2");
    assert.deepEqual(await memberships.page(undefined, 10), [
      { restaurantId: "r-1", ownerId: "owner", staff: ["cook"] },
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
    assert.equal(record?.publishedAt, null);
    assert.equal(record?.attempts, 0);
    assert.equal(record?.topic, "restaurants.test");
    assert.equal(record?.metadata.traceparent, "00-abc-def-01");
    assert.equal(
      Buffer.from(record?.payload.buffer).toString(),
      "RestaurantOrderReceived",
    );
    assert.ok(record?.seq >= 1);

    // Same aggregate version and type again: duplicate eventId aborts.
    await assert.rejects(
      unitOfWork.run(() =>
        outbox.add({ type: "RestaurantOrder", id: order.id, version: 1 }, [
          { type: "RestaurantOrderReceived", occurredAt: now },
        ]),
      ),
      (error) => error instanceof MongoServerError && error.code === 11000,
    );

    // seq grows monotonically across transactions.
    const next = receive();
    await unitOfWork.run(async () => {
      const version = await repository.save(next);
      await outbox.add(
        { type: "RestaurantOrder", id: next.id, version },
        next.pullEvents(),
      );
    });
    const nextRecord = await mongo.db
      .collection(OUTBOX_COLLECTION)
      .findOne({ aggregateId: next.id });
    assert.ok((nextRecord?.seq ?? 0) > (record?.seq ?? 0));
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

  test("outbox refuses records outside a unit of work", async () => {
    const outbox = new MongoOutbox(mongo.db, unitOfWork);
    await assert.rejects(
      outbox.add({ type: "RestaurantOrder", id: "x", version: 1 }, [
        { type: "X", occurredAt: now },
      ]),
      MongoOutboxOutsideUnitOfWorkError,
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
