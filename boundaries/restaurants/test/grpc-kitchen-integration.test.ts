import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";
import { setTimeout as delay } from "node:timers/promises";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { RestaurantOrderState } from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { migrateRestaurantsDatabase } from "../infrastructure/mongodb/collections.ts";
import type { Subject } from "../pkg/application/subject.ts";
import {
  connectMongoDb,
  type MongoDbConnection,
} from "../pkg/infrastructure/mongodb/client.ts";
import {
  MongoOutbox,
  OUTBOX_COLLECTION,
} from "../pkg/infrastructure/mongodb/outbox.ts";
import { MongoUnitOfWork } from "../pkg/infrastructure/mongodb/unit-of-work.ts";
import { Menu } from "../slices/menu/domains/menu/menu.ts";
import { Money } from "../slices/menu/domains/menu/vo/money.ts";
import {
  createMenuCollection,
  MongoMenuRepository,
} from "../slices/menu/infrastructure/mongodb/menus.ts";
import { Restaurant } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import {
  createRestaurantCollection,
  MongoRestaurantRepository,
} from "../slices/restaurant/infrastructure/mongodb/restaurants.ts";
import { AcceptOrderHandler } from "../slices/restaurantorder/applications/accept-order/accept-order.ts";
import { GetKitchenQueueHandler } from "../slices/restaurantorder/applications/get-kitchen-queue/get-kitchen-queue.ts";
import { ListActiveRestaurantOrdersHandler } from "../slices/restaurantorder/applications/list-active-restaurant-orders/list-active-restaurant-orders.ts";
import { ListIncomingOrdersHandler } from "../slices/restaurantorder/applications/list-incoming-orders/list-incoming-orders.ts";
import { MarkAssembledHandler } from "../slices/restaurantorder/applications/mark-assembled/mark-assembled.ts";
import { MarkReadyForPickupHandler } from "../slices/restaurantorder/applications/mark-ready-for-pickup/mark-ready-for-pickup.ts";
import { ReceiveOrderRequestHandler } from "../slices/restaurantorder/applications/receive-order-request/receive-order-request.ts";
import { RejectOrderHandler } from "../slices/restaurantorder/applications/reject-order/reject-order.ts";
import { ReportPreparationDelayHandler } from "../slices/restaurantorder/applications/report-preparation-delay/report-preparation-delay.ts";
import { ReportPreparationFailureHandler } from "../slices/restaurantorder/applications/report-preparation-failure/report-preparation-failure.ts";
import { StartPreparationHandler } from "../slices/restaurantorder/applications/start-preparation/start-preparation.ts";
import {
  FixedPreparationTimeEstimator,
  UnconstrainedAcceptanceConditions,
} from "../slices/restaurantorder/infrastructure/acceptance/mvp.ts";
import { restaurantOrderEventMapper } from "../slices/restaurantorder/infrastructure/integration/restaurant-order-events.ts";
import { FifoKitchenQueueOrdering } from "../slices/restaurantorder/infrastructure/kitchen-queue/mvp.ts";
import {
  createRestaurantOrderCollection,
  MongoKitchenBoard,
  MongoRestaurantOrderRepository,
} from "../slices/restaurantorder/infrastructure/mongodb/restaurant-orders.ts";
import {
  RepositoryAcceptanceFactsReader,
  RepositoryRestaurantExistence,
} from "../slices/restaurantorder/infrastructure/restaurant-facts/restaurant-facts.ts";
import {
  createService,
  type RestaurantsServiceUnderTest,
} from "./support/grpc.ts";
import { TestKitchenAccessControl } from "./support/kitchen.ts";
import { mongoUri, noKitchenChanges } from "./support/mongodb.ts";

// Kitchen command RPCs composed as in cmd/restaurants/main.ts (MongoDB Unit of
// Work, MongoOutbox with the Integration Event mapper) against a replica set:
// MongoDB is the testcontainer of the global setup (test/support/global-containers.ts).
const uri = mongoUri();

const context = undefined as never;
const cook: Subject = { type: "user", id: "cook" };
const ordersService: Subject = { type: "service", id: "orders-worker" };

describe("kitchen command RPCs over MongoDB", () => {
  let mongo: MongoDbConnection;
  let service: RestaurantsServiceUnderTest;
  let receive: ReceiveOrderRequestHandler;
  const restaurantId = `r-${randomUUID()}`;

  before(async () => {
    mongo = await connectMongoDb({
      enabled: true,
      uri,
      database: `restaurants_kitchen_${randomUUID().slice(0, 8)}`,
      timeoutMs: 5000,
    });
    await migrateRestaurantsDatabase(mongo.db);
    const unitOfWork = new MongoUnitOfWork(mongo.client);
    const outbox = new MongoOutbox(mongo.db, unitOfWork, {
      mapper: restaurantOrderEventMapper,
    });
    const restaurants = new MongoRestaurantRepository(
      createRestaurantCollection(mongo.db, unitOfWork),
      noKitchenChanges,
    );
    const menus = new MongoMenuRepository(
      createMenuCollection(mongo.db, unitOfWork),
    );
    const restaurantOrders = new MongoRestaurantOrderRepository(
      createRestaurantOrderCollection(mongo.db, unitOfWork),
      noKitchenChanges,
    );
    const board = new MongoKitchenBoard(
      createRestaurantOrderCollection(mongo.db, unitOfWork),
    );
    const clock = { now: () => new Date() };
    const accessControl = new TestKitchenAccessControl();
    accessControl.grantStaff(cook, restaurantId);
    accessControl.grantFulfillment(ordersService, restaurantId);

    const restaurant = Restaurant.register({
      id: restaurantId,
      name: "Pho House",
      ownerId: "owner",
      address: "1 Pho Street",
      registeredAt: clock.now(),
    });
    restaurant.open(clock.now());
    await restaurants.save(restaurant);
    const menu = Menu.create({
      id: `m-${randomUUID()}`,
      restaurantId,
      createdAt: clock.now(),
    });
    menu.addItem({
      id: "pho",
      name: "Pho",
      price: Money.from({ amount: "4.50", currency: "USD" }),
      isAvailable: true,
      addedAt: clock.now(),
    });
    await menus.save(menu);

    const commands = {
      accessControl,
      unitOfWork,
      restaurantOrders,
      outbox,
      clock,
    };
    receive = new ReceiveOrderRequestHandler({
      accessControl,
      restaurants: new RepositoryRestaurantExistence(restaurants),
      restaurantOrders,
      ids: { next: () => randomUUID() },
      clock,
    });
    const unused = {
      execute: async (): Promise<never> => assert.fail("unexpected call"),
    };
    service = createService({
      subjects: { resolve: async () => cook },
      getRestaurant: unused,
      getMenu: unused,
      listRestaurants: unused,
      listOwnedRestaurants: unused,
      receiveOrderRequest: unused,
      cancelRestaurantOrder: unused,
      registerRestaurant: unused,
      assignStaff: unused,
      revokeStaff: unused,
      openRestaurant: unused,
      closeRestaurant: unused,
      createMenu: unused,
      addMenuItem: unused,
      updateMenuItem: unused,
      setMenuItemAvailability: unused,
      acceptRestaurantOrder: new AcceptOrderHandler({
        ...commands,
        facts: new RepositoryAcceptanceFactsReader({ restaurants, menus }),
        conditions: new UnconstrainedAcceptanceConditions(),
        readyTimeEstimator: new FixedPreparationTimeEstimator(20 * 60_000),
      }),
      rejectRestaurantOrder: new RejectOrderHandler(commands),
      startPreparation: new StartPreparationHandler({
        ...commands,
        onRepeat: "succeed-without-event",
      }),
      markRestaurantOrderAssembled: new MarkAssembledHandler({
        accessControl,
        restaurantOrders,
        clock,
      }),
      markRestaurantOrderReady: new MarkReadyForPickupHandler({
        ...commands,
        onRepeat: "succeed-without-event",
      }),
      reportPreparationDelay: new ReportPreparationDelayHandler(commands),
      reportPreparationFailure: new ReportPreparationFailureHandler(commands),
      getKitchenQueue: new GetKitchenQueueHandler({
        accessControl,
        reader: board,
        ordering: new FifoKitchenQueueOrdering(),
      }),
      listIncomingOrders: new ListIncomingOrdersHandler({
        accessControl,
        reader: board,
      }),
      listActiveRestaurantOrders: new ListActiveRestaurantOrdersHandler({
        accessControl,
        reader: board,
      }),
    });
  });

  after(async () => {
    if (mongo === undefined) return;
    await mongo.db.dropDatabase();
    await mongo.close();
  });

  const received = async (orderId: string) => {
    const { restaurantOrder } = await receive.execute({
      subject: ordersService,
      restaurantId,
      orderId,
      items: [{ menuItemId: "pho", quantity: 1 }],
    });
    return {
      restaurantId,
      restaurantOrderId: restaurantOrder.restaurantOrderId,
    };
  };

  const outboxEventTypes = async (aggregateId: string) =>
    (
      await mongo.db
        .collection(OUTBOX_COLLECTION)
        .find({ aggregateId })
        .sort({ aggregateVersion: 1 })
        .toArray()
    ).map((record) => record.eventType);

  test("AC-540 the kitchen flow writes Integration Events to the Outbox", async () => {
    const order = await received("order-flow");

    await service.acceptRestaurantOrder(order, context);
    await service.startPreparation(order, context);
    await service.reportPreparationDelay(
      {
        ...order,
        expectedReadyAt: timestampFromDate(new Date(Date.now() + 3_600_000)),
      },
      context,
    );
    await service.markRestaurantOrderAssembled(order, context);
    const ready = await service.markRestaurantOrderReady(order, context);

    assert.equal(ready.restaurantOrder?.state, RestaurantOrderState.READY);
    assert.deepEqual(await outboxEventTypes(order.restaurantOrderId), [
      "nomnom.restaurants.v1.RestaurantOrderAccepted",
      "nomnom.restaurants.v1.PreparationStarted",
      "nomnom.restaurants.v1.PreparationDelayed",
      "nomnom.restaurants.v1.RestaurantOrderReady",
    ]);
  });

  test("AC-742 ListIncomingOrders lists received orders by received_at until Acceptance moves them to GetKitchenQueue", async () => {
    const first = await received("order-incoming-1");
    // Distinct received_at, so the order is by time and not by random id.
    await delay(5);
    const second = await received("order-incoming-2");
    const ids = new Set([first.restaurantOrderId, second.restaurantOrderId]);
    const incoming = async () =>
      (
        await service.listIncomingOrders({ restaurantId }, context)
      ).orders.filter((order) =>
        ids.has(order.restaurantOrder?.restaurantOrderId ?? ""),
      );
    const incomingIds = async () =>
      (await incoming()).map(
        (order) => order.restaurantOrder?.restaurantOrderId,
      );

    const waiting = await incoming();
    assert.deepEqual(
      waiting.map((order) => order.restaurantOrder?.restaurantOrderId),
      [first.restaurantOrderId, second.restaurantOrderId],
    );
    assert.equal(waiting[0]?.restaurantOrder?.orderId, "order-incoming-1");
    assert.equal(waiting[0]?.restaurantOrder?.version, 1n);
    assert.deepEqual(
      waiting[0]?.items.map((item) => [item.menuItemId, item.quantity]),
      [["pho", 1]],
    );
    const [firstAt, secondAt] = waiting.map((order) =>
      order.receivedAt === undefined
        ? 0
        : timestampDate(order.receivedAt).getTime(),
    );
    assert.ok((firstAt ?? 0) > 0 && (firstAt ?? 0) < (secondAt ?? 0));
    await service.acceptRestaurantOrder(first, context);

    assert.deepEqual(await incomingIds(), [second.restaurantOrderId]);
    const queue = await service.getKitchenQueue({ restaurantId }, context);
    assert.ok(
      queue.entries.some(
        (entry) =>
          entry.restaurantOrder?.restaurantOrderId === first.restaurantOrderId,
      ),
    );
  });

  test("ListActiveRestaurantOrders lists the kitchen work from receipt to READY by received_at", async () => {
    const queued = await received("order-active-queued");
    await delay(5);
    const ready = await received("order-active-ready");
    await delay(5);
    const waiting = await received("order-active-received");
    await delay(5);
    const rejected = await received("order-active-rejected");
    await service.acceptRestaurantOrder(queued, context);
    await service.acceptRestaurantOrder(ready, context);
    await service.startPreparation(ready, context);
    await service.markRestaurantOrderAssembled(ready, context);
    await service.markRestaurantOrderReady(ready, context);
    await service.rejectRestaurantOrder(rejected, context);
    const ids = new Set(
      [queued, ready, waiting, rejected].map(
        (order) => order.restaurantOrderId,
      ),
    );

    const active = (
      await service.listActiveRestaurantOrders({ restaurantId }, context)
    ).orders.filter((order) =>
      ids.has(order.restaurantOrder?.restaurantOrderId ?? ""),
    );

    assert.deepEqual(
      active.map((order) => [
        order.restaurantOrder?.restaurantOrderId,
        order.restaurantOrder?.state,
      ]),
      [
        [queued.restaurantOrderId, RestaurantOrderState.QUEUED],
        [ready.restaurantOrderId, RestaurantOrderState.READY],
        [waiting.restaurantOrderId, RestaurantOrderState.RECEIVED],
      ],
    );
    const [first] = active;
    assert.equal(first?.restaurantOrder?.orderId, "order-active-queued");
    assert.equal(first?.restaurantOrder?.restaurantId, restaurantId);
    assert.ok(first?.restaurantOrder?.expectedReadyAt !== undefined);
    assert.ok(first?.receivedAt !== undefined);
    assert.deepEqual(
      first?.items.map((item) => [item.menuItemId, item.quantity]),
      [["pho", 1]],
    );
  });

  test("AC-540 RejectRestaurantOrder and ReportPreparationFailure write their facts", async () => {
    const rejected = await received("order-rejected");
    await service.rejectRestaurantOrder(
      { ...rejected, reason: "too_busy" },
      context,
    );
    const failed = await received("order-failed");
    await service.acceptRestaurantOrder(failed, context);
    await service.startPreparation(failed, context);
    await service.reportPreparationFailure(
      { ...failed, reason: "ITEM_UNAVAILABLE" },
      context,
    );

    assert.deepEqual(await outboxEventTypes(rejected.restaurantOrderId), [
      "nomnom.restaurants.v1.RestaurantOrderRejected",
    ]);
    assert.deepEqual(await outboxEventTypes(failed.restaurantOrderId), [
      "nomnom.restaurants.v1.RestaurantOrderAccepted",
      "nomnom.restaurants.v1.PreparationStarted",
      "nomnom.restaurants.v1.PreparationFailed",
    ]);
  });
});
