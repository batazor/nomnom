import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";

import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import { RestaurantOrderState } from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { createRestaurantsService } from "../infrastructure/grpc/service.ts";
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
import { AcceptOrderHandler } from "../slices/kitchenorder/applications/accept-order/accept-order.ts";
import { MarkAssembledHandler } from "../slices/kitchenorder/applications/mark-assembled/mark-assembled.ts";
import { MarkReadyForPickupHandler } from "../slices/kitchenorder/applications/mark-order-ready/mark-order-ready.ts";
import { ReceiveOrderRequestHandler } from "../slices/kitchenorder/applications/receive-order-request/receive-order-request.ts";
import { RejectOrderHandler } from "../slices/kitchenorder/applications/reject-order/reject-order.ts";
import { ReportPreparationDelayHandler } from "../slices/kitchenorder/applications/report-preparation-delay/report-preparation-delay.ts";
import { ReportPreparationFailureHandler } from "../slices/kitchenorder/applications/report-preparation-failure/report-preparation-failure.ts";
import { StartPreparationHandler } from "../slices/kitchenorder/applications/start-preparing-order/start-preparing-order.ts";
import {
  FixedPreparationTimeEstimator,
  UnconstrainedAcceptanceConditions,
} from "../slices/kitchenorder/infrastructure/acceptance/mvp.ts";
import { restaurantOrderEventMapper } from "../slices/kitchenorder/infrastructure/integration/restaurant-order-events.ts";
import { InMemoryKitchenAccessControl } from "../slices/kitchenorder/infrastructure/memory/restaurant-orders.ts";
import {
  createRestaurantOrderCollection,
  MongoRestaurantOrderRepository,
} from "../slices/kitchenorder/infrastructure/mongodb/restaurant-orders.ts";
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

// Kitchen command RPCs composed as in cmd/restaurants/main.ts (MongoDB Unit of
// Work, MongoOutbox with the Integration Event mapper) against a replica set:
// MONGODB_URI=mongodb://localhost:27017/?replicaSet=rs0&directConnection=true
const uri = process.env.MONGODB_URI?.trim() ?? "";
const skip = uri === "" ? "MONGODB_URI is not set" : false;

const context = undefined as never;
const cook: Subject = { type: "user", id: "cook" };
const ordersService: Subject = { type: "service", id: "orders-worker" };

describe("kitchen command RPCs over MongoDB", { skip }, () => {
  let mongo: MongoDbConnection;
  let service: ReturnType<typeof createRestaurantsService>;
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
    );
    const menus = new MongoMenuRepository(
      createMenuCollection(mongo.db, unitOfWork),
    );
    const restaurantOrders = new MongoRestaurantOrderRepository(
      createRestaurantOrderCollection(mongo.db, unitOfWork),
    );
    const clock = { now: () => new Date() };
    const accessControl = new InMemoryKitchenAccessControl();
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
      restaurants,
      restaurantOrders,
      ids: { next: () => randomUUID() },
      clock,
    });
    const unused = {
      execute: async (): Promise<never> => assert.fail("unexpected call"),
    };
    service = createRestaurantsService({
      subjects: { resolve: async () => cook },
      getRestaurant: unused,
      receiveOrderRequest: unused,
      cancelRestaurantOrder: unused,
      registerRestaurant: unused,
      assignStaff: unused,
      revokeStaff: unused,
      acceptRestaurantOrder: new AcceptOrderHandler({
        ...commands,
        restaurants,
        menus,
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
