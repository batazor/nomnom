import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";
import { setTimeout as delay } from "node:timers/promises";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { RestaurantOrderState } from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import {
  connectDatabase,
  type DatabaseConnection,
} from "../infrastructure/repository/client.ts";
import { migrateRestaurantsDatabase } from "../infrastructure/repository/migrate.ts";
import {
  DatabaseOutbox,
  OUTBOX_COLLECTION,
} from "../infrastructure/repository/outbox.ts";
import { DatabaseUnitOfWork } from "../infrastructure/repository/unit-of-work.ts";
import type { Subject } from "../pkg/authz/subject.ts";
import { PoolCookAssigner } from "../slices/cook/applications/assign/assign.ts";
import { Cook } from "../slices/cook/domains/cook/cook.ts";
import { DatabaseCookRepository } from "../slices/cook/infrastructure/repository/cooks.ts";
import { Menu } from "../slices/menu/domains/menu/menu.ts";
import {
  createMenuCollection,
  DatabaseMenuRepository,
} from "../slices/menu/infrastructure/repository/menus.ts";
import { Restaurant } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import { Money } from "../slices/restaurant/domains/restaurant/vo/money.ts";
import {
  createRestaurantCollection,
  DatabaseRestaurantRepository,
  DatabaseRestaurantWaitTimes,
} from "../slices/restaurant/infrastructure/repository/restaurants.ts";
import { AcceptOrderHandler } from "../slices/restaurantorder/applications/acceptance/accept/accept.ts";
import { ReceiveOrderRequestHandler } from "../slices/restaurantorder/applications/acceptance/receive-request/receive-request.ts";
import { RejectOrderHandler } from "../slices/restaurantorder/applications/acceptance/reject/reject.ts";
import { GetKitchenQueueHandler } from "../slices/restaurantorder/applications/kitchen-board/get-queue/get-queue.ts";
import { ListActiveRestaurantOrdersHandler } from "../slices/restaurantorder/applications/kitchen-board/list-active/list-active.ts";
import { ListIncomingOrdersHandler } from "../slices/restaurantorder/applications/kitchen-board/list-incoming/list-incoming.ts";
import { MarkAssembledHandler } from "../slices/restaurantorder/applications/preparation/mark-assembled/mark-assembled.ts";
import { MarkReadyForPickupHandler } from "../slices/restaurantorder/applications/preparation/mark-ready-for-pickup/mark-ready-for-pickup.ts";
import { ReportPreparationDelayHandler } from "../slices/restaurantorder/applications/preparation/report-delay/report-delay.ts";
import { ReportPreparationFailureHandler } from "../slices/restaurantorder/applications/preparation/report-failure/report-failure.ts";
import { StartPreparationHandler } from "../slices/restaurantorder/applications/preparation/start/start.ts";
import { OpeningHoursAcceptanceConditions } from "../slices/restaurantorder/infrastructure/acceptance/opening-hours.ts";
import { WaitTimeReadyTimeEstimator } from "../slices/restaurantorder/infrastructure/acceptance/wait-time.ts";
import { restaurantOrderEventMapper } from "../slices/restaurantorder/infrastructure/integration/restaurant-order-events.ts";
import { PriorityKitchenQueueOrdering } from "../slices/restaurantorder/infrastructure/kitchen-queue/priority.ts";
import {
  createRestaurantOrderCollection,
  DatabaseKitchenBoard,
  DatabaseRestaurantOrderRepository,
} from "../slices/restaurantorder/infrastructure/repository/restaurant-orders.ts";
import {
  RepositoryAcceptanceFactsReader,
  RepositoryRestaurantExistence,
} from "../slices/restaurantorder/infrastructure/restaurant-facts/restaurant-facts.ts";
import { databaseUri, noKitchenChanges } from "./support/database.ts";
import {
  createService,
  type RestaurantsServiceUnderTest,
} from "./support/grpc.ts";
import { TestKitchenAccessControl } from "./support/kitchen.ts";
import { ALWAYS_OPEN, testWaitTimePolicy } from "./support/restaurants.ts";

// Kitchen command RPCs composed as in cmd/restaurants/main.ts (MongoDB Unit of
// Work, DatabaseOutbox with the Integration Event mapper) against a replica set:
// MongoDB is the testcontainer of the global setup (test/support/global-containers.ts).
const uri = databaseUri();

const context = undefined as never;
const cook: Subject = { type: "user", id: "cook" };
const ordersService: Subject = { type: "service", id: "orders-worker" };

describe("kitchen command RPCs over MongoDB", () => {
  let database: DatabaseConnection;
  let service: RestaurantsServiceUnderTest;
  let receive: ReceiveOrderRequestHandler;
  const restaurantId = `r-${randomUUID()}`;

  before(async () => {
    database = await connectDatabase({
      uri,
      database: `restaurants_kitchen_${randomUUID().slice(0, 8)}`,
      timeoutMs: 5000,
    });
    await migrateRestaurantsDatabase(database.db);
    const unitOfWork = new DatabaseUnitOfWork(database.client);
    const outbox = new DatabaseOutbox(database.db, unitOfWork, {
      mapper: restaurantOrderEventMapper,
    });
    const restaurants = new DatabaseRestaurantRepository(
      createRestaurantCollection(database.db, unitOfWork),
      noKitchenChanges,
    );
    const menus = new DatabaseMenuRepository(
      createMenuCollection(database.db, unitOfWork),
    );
    const restaurantOrders = new DatabaseRestaurantOrderRepository(
      createRestaurantOrderCollection(database.db, unitOfWork),
      noKitchenChanges,
    );
    const board = new DatabaseKitchenBoard(
      createRestaurantOrderCollection(database.db, unitOfWork),
    );
    const clock = { now: () => new Date() };
    const accessControl = new TestKitchenAccessControl();
    accessControl.grantStaff(cook, restaurantId);
    accessControl.grantFulfillment(ordersService, restaurantId);

    const restaurant = Restaurant.register({
      id: restaurantId,
      name: "Pho House",
      ownerId: "owner",
      marketId: "berlin",
      timeZone: "Europe/Berlin",
      registeredAt: clock.now(),
    });
    restaurant.open(clock.now());
    restaurant.setOpeningHours(ALWAYS_OPEN);
    await restaurants.save(restaurant);
    const menu = Menu.create({
      id: `m-${randomUUID()}`,
      restaurantId,
      marketId: "berlin",
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
    // A line wide enough for every order the tests start at once.
    const cooks = new DatabaseCookRepository(
      database.db,
      unitOfWork,
      noKitchenChanges,
    );
    for (const name of ["Aigul", "Oleg", "Marat", "Timur"]) {
      const hired = Cook.hire({ id: `cook-${name}`, restaurantId, name });
      hired.putOnLine();
      await cooks.save(hired);
    }

    const commands = {
      accessControl,
      unitOfWork,
      restaurantOrders,
      outbox,
      clock,
    };
    const ordering = new PriorityKitchenQueueOrdering({
      store: new DatabaseRestaurantWaitTimes(
        createRestaurantCollection(database.db, unitOfWork),
      ),
      policy: testWaitTimePolicy(),
      agingFactor: 2,
    });
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
      batchGetRestaurants: unused,
      batchGetMenus: unused,
      listRestaurants: unused,
      listOwnedRestaurants: unused,
      receiveOrderRequest: unused,
      cancelRestaurantOrder: unused,
      registerRestaurant: unused,
      updateRestaurant: unused,
      assignStaff: unused,
      revokeStaff: unused,
      openRestaurant: unused,
      closeRestaurant: unused,
      createMenu: unused,
      addMenuItem: unused,
      updateMenuItem: unused,
      setMenuItemAvailability: unused,
      getSimulator: { execute: () => Promise.reject(new Error("unused")) },
      setOpeningHours: unused,
      setScheduledOrderSettings: unused,
      queueRestaurantOrder: unused,
      recordPickup: unused,
      listScheduledSlots: unused,
      reserveScheduledSlot: unused,
      confirmScheduledSlot: unused,
      releaseScheduledSlot: unused,
      listScheduledOrders: unused,
      setSimulatorEnabled: {
        execute: () => Promise.reject(new Error("unused")),
      },
      hireCook: {
        execute: () => Promise.reject(new Error("unused")),
      },
      putCookOnLine: {
        execute: () => Promise.reject(new Error("unused")),
      },
      takeCookOffLine: {
        execute: () => Promise.reject(new Error("unused")),
      },
      retireCook: {
        execute: () => Promise.reject(new Error("unused")),
      },
      getCookPool: {
        execute: () => Promise.reject(new Error("unused")),
      },
      acceptRestaurantOrder: new AcceptOrderHandler({
        ...commands,
        facts: new RepositoryAcceptanceFactsReader({ restaurants, menus }),
        conditions: new OpeningHoursAcceptanceConditions(restaurants),
        readyTimeEstimator: new WaitTimeReadyTimeEstimator({
          store: new DatabaseRestaurantWaitTimes(
            createRestaurantCollection(database.db, unitOfWork),
          ),
          policy: testWaitTimePolicy(),
        }),
      }),
      rejectRestaurantOrder: new RejectOrderHandler(commands),
      startPreparation: new StartPreparationHandler({
        ...commands,
        started: board,
        queue: board,
        ordering,
        cooks: new PoolCookAssigner({ cooks, workload: board }),
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
        ordering,
        clock,
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
    if (database === undefined) return;
    await database.db.dropDatabase();
    await database.close();
  });

  const received = async (orderId: string) => {
    const { restaurantOrder } = await receive.execute({
      subject: ordersService,
      restaurantId,
      orderId,
      items: [{ menuItemId: "pho", quantity: 1 }],
      priority: "STANDARD",
    });
    return {
      restaurantId,
      restaurantOrderId: restaurantOrder.restaurantOrderId,
    };
  };

  /**
   * StartNextPreparation until `order` is started: the tests share one
   * restaurant, so orders other tests left in the queue are taken first and
   * finished to free their cooks.
   */
  const startHead = async (order: {
    restaurantId: string;
    restaurantOrderId: string;
  }) => {
    for (;;) {
      const { restaurantOrder } = await service.startNextPreparation(
        { restaurantId, requestId: randomUUID() },
        context,
      );
      assert.ok(restaurantOrder, `${order.restaurantOrderId} is not queued`);
      if (restaurantOrder.restaurantOrderId === order.restaurantOrderId) return;
      const leftover = {
        restaurantId,
        restaurantOrderId: restaurantOrder.restaurantOrderId,
      };
      await service.markRestaurantOrderAssembled(leftover, context);
      await service.markRestaurantOrderReady(leftover, context);
    }
  };

  const outboxEventTypes = async (aggregateId: string) =>
    (
      await database.db
        .collection(OUTBOX_COLLECTION)
        .find({ aggregateId })
        .sort({ aggregateVersion: 1 })
        .toArray()
    ).map((record) => record.eventType);

  test("AC-540 the kitchen flow writes Integration Events to the Outbox", async () => {
    const order = await received("order-flow");

    await service.acceptRestaurantOrder(order, context);
    await startHead(order);
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
    // Accepted after `ready` started: the queue head is never named.
    await service.acceptRestaurantOrder(ready, context);
    await startHead(ready);
    await service.markRestaurantOrderAssembled(ready, context);
    await service.markRestaurantOrderReady(ready, context);
    await service.rejectRestaurantOrder(rejected, context);
    await service.acceptRestaurantOrder(queued, context);
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
    const expectedReadyAt = first?.restaurantOrder?.expectedReadyAt;
    assert.ok(expectedReadyAt !== undefined);
    assert.ok(first?.receivedAt !== undefined);
    assert.ok(first.acceptedAt !== undefined);
    assert.ok(
      timestampDate(expectedReadyAt).getTime() >
        timestampDate(first.acceptedAt).getTime(),
    );
    assert.equal(active.at(-1)?.acceptedAt, undefined);
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
    await startHead(failed);
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
