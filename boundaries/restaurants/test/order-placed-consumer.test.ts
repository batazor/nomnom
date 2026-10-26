import assert from "node:assert/strict";
import test from "node:test";

import { create, toBinary } from "@bufbuild/protobuf";

import { OrderPlacedSchema } from "../infrastructure/grpc/gen/nomnom/orders/v1/events_pb.js";
import type {
  InboundMessage,
  IntegrationMessage,
} from "../pkg/infrastructure/messaging/envelope.ts";
import { MessageProcessor } from "../pkg/infrastructure/messaging/processor.ts";
import {
  INBOX_COLLECTION,
  MongoInbox,
} from "../pkg/infrastructure/repository/inbox.ts";
import { ReceiveOrderRequestHandler } from "../slices/restaurantorder/applications/receive-order-request/receive-order-request.ts";
import { RestaurantOrderState } from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import {
  createOrderPlacedHandler,
  ORDER_EVENTS_TOPIC,
  ORDER_PLACED_CONSUMER,
  ORDER_PLACED_EVENT_TYPE,
  ORDERS_EVENT_SOURCE,
  OrdersEventSourceAccessControl,
} from "../slices/restaurantorder/infrastructure/integration/order-placed.ts";
import { RepositoryRestaurantExistence } from "../slices/restaurantorder/infrastructure/restaurant-facts/restaurant-facts.ts";
import { createKitchenWorld, ordersService } from "./support/kitchen.ts";
import {
  createInfrastructure,
  SequentialIdGenerator,
} from "./support/restaurants.ts";

type Item = { menuItemId: string; quantity: number };

const orderPlaced = (
  fields: { orderId?: string; restaurantId: string; items: Item[] },
  eventId = "event-1",
): InboundMessage => {
  const orderId = fields.orderId ?? "order-1";
  return {
    topic: ORDER_EVENTS_TOPIC,
    partition: 0,
    offset: "0",
    key: Buffer.from(orderId),
    value: toBinary(
      OrderPlacedSchema,
      create(OrderPlacedSchema, {
        orderId,
        customerId: "customer-1",
        restaurantId: fields.restaurantId,
        total: { amount: "19.00", currency: "EUR" },
        items: fields.items.map((item) => ({
          ...item,
          unitPrice: { amount: "9.50", currency: "EUR" },
        })),
      }),
    ),
    headers: {
      event_id: eventId,
      event_type: ORDER_PLACED_EVENT_TYPE,
      occurred_at: "2026-10-25T09:00:00Z",
      aggregate_type: "order",
      aggregate_id: orderId,
      aggregate_version: "1",
    },
  };
};

/** Consumer over the MongoDB kitchen with the event source access control. */
const setup = async () => {
  const infrastructure = await createInfrastructure();
  const world = await createKitchenWorld({ infrastructure });
  const kitchen = await world.givenKitchen();
  const receiveOrderRequest = new ReceiveOrderRequestHandler({
    accessControl: new OrdersEventSourceAccessControl(),
    restaurants: new RepositoryRestaurantExistence(world.restaurants),
    restaurantOrders: world.restaurantOrders,
    ids: new SequentialIdGenerator("restaurant-order"),
    clock: infrastructure.clock,
  });
  const inbox = new MongoInbox(infrastructure.db, infrastructure.unitOfWork);
  const deadLetters: IntegrationMessage[] = [];
  const processor = new MessageProcessor({
    consumer: ORDER_PLACED_CONSUMER,
    group: "restaurants",
    handlers: [createOrderPlacedHandler({ receiveOrderRequest })],
    inbox,
    deadLetters: {
      publish: async (dead) => {
        deadLetters.push(dead);
      },
    },
    retry: { maxRetries: 0, initialIntervalMs: 1, maxIntervalMs: 1 },
    log: () => {},
  });
  return { world, kitchen, processor, deadLetters };
};

test("OrderPlaced creates a RECEIVED Restaurant Order with the placed items", async () => {
  const { world, kitchen, processor } = await setup();

  const outcome = await processor.process(
    orderPlaced({
      restaurantId: kitchen.restaurantId,
      items: [{ menuItemId: kitchen.phoId, quantity: 2 }],
    }),
  );

  assert.equal(outcome, "processed");
  const order = await world.restaurantOrders.findByOrderId("order-1");
  assert.ok(order);
  const snapshot = order.toSnapshot();
  assert.equal(snapshot.state, RestaurantOrderState.Received);
  assert.equal(snapshot.restaurantId, kitchen.restaurantId);
  assert.deepEqual(
    snapshot.items.map((item) => [item.menuItemId, item.quantity]),
    [[kitchen.phoId, 2]],
  );
});

test("a redelivered OrderPlaced creates the Restaurant Order once", async () => {
  const { world, kitchen, processor } = await setup();
  const event = orderPlaced({
    restaurantId: kitchen.restaurantId,
    items: [{ menuItemId: kitchen.phoId, quantity: 2 }],
  });

  assert.equal(await processor.process(event), "processed");
  assert.equal(await processor.process(event), "duplicate");

  assert.equal((await world.orderStore.find({})).length, 1);
  assert.equal(await world.db.collection(INBOX_COLLECTION).countDocuments(), 1);
});

test("OrderPlaced after ReceiveOrderRequest of the fulfillment process converges on one order", async () => {
  const { world, kitchen, processor } = await setup();
  const viaRpc = await world.receive.execute({
    subject: ordersService,
    restaurantId: kitchen.restaurantId,
    orderId: "order-1",
    items: [{ menuItemId: kitchen.phoId, quantity: 2 }],
  });

  const outcome = await processor.process(
    orderPlaced({
      restaurantId: kitchen.restaurantId,
      items: [{ menuItemId: kitchen.phoId, quantity: 2 }],
    }),
  );

  assert.equal(outcome, "processed");
  assert.equal((await world.orderStore.find({})).length, 1);
  assert.equal(
    (await world.restaurantOrders.findByOrderId("order-1"))?.id,
    viaRpc.restaurantOrder.restaurantOrderId,
  );
});

test("OrderPlaced without items is dead-lettered", async () => {
  const { world, kitchen, processor, deadLetters } = await setup();

  const outcome = await processor.process(
    orderPlaced({ restaurantId: kitchen.restaurantId, items: [] }),
  );

  assert.equal(outcome, "dead-lettered");
  assert.equal(deadLetters[0]?.topic, `${ORDER_EVENTS_TOPIC}.dlq`);
  assert.match(deadLetters[0]?.headers.reason_poisoned ?? "", /no items/);
  assert.equal((await world.orderStore.find({})).length, 0);
});

test("an OrderPlaced payload that is not Protobuf is dead-lettered", async () => {
  const { processor, deadLetters } = await setup();
  const event = orderPlaced({ restaurantId: "r", items: [] });

  const outcome = await processor.process({
    ...event,
    value: new Uint8Array([0xff, 0xff, 0xff]),
  });

  assert.equal(outcome, "dead-lettered");
  assert.equal(deadLetters.length, 1);
});

test("an invalid quantity is dead-lettered without a Restaurant Order", async () => {
  const { world, kitchen, processor, deadLetters } = await setup();

  const outcome = await processor.process(
    orderPlaced({
      restaurantId: kitchen.restaurantId,
      items: [{ menuItemId: kitchen.phoId, quantity: 0 }],
    }),
  );

  assert.equal(outcome, "dead-lettered");
  assert.equal(deadLetters.length, 1);
  assert.equal((await world.orderStore.find({})).length, 0);
});

test("OrderPlaced for an unknown restaurant is a business outcome", async () => {
  const { world, processor, deadLetters } = await setup();

  const outcome = await processor.process(
    orderPlaced({
      restaurantId: "missing",
      items: [{ menuItemId: "pho", quantity: 1 }],
    }),
  );

  assert.equal(outcome, "rejected");
  assert.equal(deadLetters.length, 0);
  assert.equal(await world.db.collection(INBOX_COLLECTION).countDocuments(), 0);
  assert.equal((await world.orderStore.find({})).length, 0);
});

test("a conflicting composition for an existing order is a business outcome", async () => {
  const { world, kitchen, processor } = await setup();
  await processor.process(
    orderPlaced({
      restaurantId: kitchen.restaurantId,
      items: [{ menuItemId: kitchen.phoId, quantity: 2 }],
    }),
  );

  const outcome = await processor.process(
    orderPlaced(
      {
        restaurantId: kitchen.restaurantId,
        items: [{ menuItemId: kitchen.phoId, quantity: 5 }],
      },
      "event-2",
    ),
  );

  assert.equal(outcome, "rejected");
  assert.equal((await world.orderStore.find({})).length, 1);
});

test("the event source access control allows only fulfillment from the orders stream", async () => {
  const accessControl = new OrdersEventSourceAccessControl();

  assert.equal(
    await accessControl.canCoordinateFulfillment(ORDERS_EVENT_SOURCE),
    true,
  );
  assert.equal(
    await accessControl.canCoordinateFulfillment({
      type: "user",
      id: "orders",
    }),
    false,
  );
  // AC-638: the adapter implements only FulfillmentCoordinatorAccess.
  assert.equal("canOperateKitchen" in accessControl, false);
  assert.equal("canViewRestaurantOrders" in accessControl, false);
});
