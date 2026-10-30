import assert from "node:assert/strict";
import test from "node:test";

import { create, toBinary } from "@bufbuild/protobuf";

import { DeliveryPickedUpSchema } from "../infrastructure/grpc/gen/nomnom/delivery/v1/events_pb.js";
import type {
  InboundMessage,
  IntegrationMessage,
} from "../pkg/infrastructure/messaging/envelope.ts";
import { MessageProcessor } from "../pkg/infrastructure/messaging/processor.ts";
import { DatabaseInbox } from "../pkg/infrastructure/repository/inbox.ts";
import { RecordPickupHandler } from "../slices/restaurantorder/applications/record-pickup/record-pickup.ts";
import { RestaurantOrderState } from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import {
  createDeliveryPickedUpHandler,
  DELIVERY_EVENTS_TOPIC,
  DELIVERY_PICKED_UP_CONSUMER,
  DELIVERY_PICKED_UP_EVENT_TYPE,
  DeliveryEventSourceAccessControl,
} from "../slices/restaurantorder/infrastructure/integration/delivery-picked-up.ts";
import { cook, createKitchenWorld } from "./support/kitchen.ts";
import { createInfrastructure } from "./support/restaurants.ts";
import { framed, SCHEMA_ID, TestSchemas } from "./support/schemas.ts";

const PICKED_UP_AT = "2026-10-26T16:50:00.000Z";

const deliveryPickedUp = (
  orderId: string,
  options: { marketId?: string; eventId?: string } = {},
): InboundMessage => ({
  topic: DELIVERY_EVENTS_TOPIC,
  partition: 0,
  offset: "0",
  key: Buffer.from("delivery-1"),
  value: framed(
    toBinary(
      DeliveryPickedUpSchema,
      create(DeliveryPickedUpSchema, {
        orderId,
        deliveryId: "delivery-1",
        marketId: options.marketId ?? "berlin",
      }),
    ),
  ),
  headers: {
    event_id: options.eventId ?? "event-1",
    event_type: DELIVERY_PICKED_UP_EVENT_TYPE,
    occurred_at: PICKED_UP_AT,
    aggregate_type: "delivery",
    aggregate_id: "delivery-1",
    aggregate_version: "3",
  },
});

/** Consumer of the delivery stream over the MongoDB kitchen. */
const setup = async () => {
  const infrastructure = await createInfrastructure();
  const world = await createKitchenWorld({ infrastructure });
  const kitchen = await world.givenKitchen();
  const recordPickup = new RecordPickupHandler({
    accessControl: new DeliveryEventSourceAccessControl(),
    unitOfWork: infrastructure.unitOfWork,
    restaurantOrders: world.restaurantOrders,
  });
  const deadLetters: IntegrationMessage[] = [];
  const processor = new MessageProcessor({
    consumer: DELIVERY_PICKED_UP_CONSUMER,
    group: "restaurants",
    handlers: [createDeliveryPickedUpHandler({ recordPickup })],
    inbox: new DatabaseInbox(infrastructure.db, infrastructure.unitOfWork),
    deadLetters: {
      publish: async (dead) => {
        deadLetters.push(dead);
      },
    },
    schemas: new TestSchemas({ [SCHEMA_ID]: [DELIVERY_PICKED_UP_EVENT_TYPE] }),
    retry: { maxRetries: 0, initialIntervalMs: 1, maxIntervalMs: 1 },
    log: () => {},
  });
  return { world, kitchen, processor, deadLetters };
};

const stateOf = async (
  world: Awaited<ReturnType<typeof setup>>["world"],
  orderId: string,
) => (await world.restaurantOrders.findByOrderId(orderId))?.toSnapshot();

test("DeliveryPickedUp takes a READY order to PICKED_UP and off the kitchen board", async () => {
  const { world, kitchen, processor } = await setup();
  const ready = await world.givenOrderIn(
    "READY",
    kitchen.restaurantId,
    kitchen.phoId,
  );

  const outcome = await processor.process(deliveryPickedUp(ready.orderId));

  assert.equal(outcome, "processed");
  const snapshot = await stateOf(world, ready.orderId);
  assert.equal(snapshot?.state, RestaurantOrderState.PickedUp);
  assert.equal(snapshot?.timeline.pickedUpAt?.toISOString(), PICKED_UP_AT);
  const board = await world.listReadyOrders.execute({
    subject: cook,
    restaurantId: kitchen.restaurantId,
  });
  assert.deepEqual(board.orders, []);
});

test("a repeated DeliveryPickedUp changes nothing", async () => {
  const { world, kitchen, processor } = await setup();
  const ready = await world.givenOrderIn(
    "READY",
    kitchen.restaurantId,
    kitchen.phoId,
  );
  await processor.process(deliveryPickedUp(ready.orderId));
  const version = (await world.restaurantOrders.findByOrderId(ready.orderId))
    ?.version;

  // A redelivery under another event id reaches the use case: a no-op.
  const outcome = await processor.process(
    deliveryPickedUp(ready.orderId, { eventId: "event-2" }),
  );

  assert.equal(outcome, "processed");
  const order = await world.restaurantOrders.findByOrderId(ready.orderId);
  assert.equal(order?.version, version);
});

test("Pickup before READY is acknowledged and leaves the order in its state", async () => {
  const { world, kitchen, processor, deadLetters } = await setup();
  const assembled = await world.givenOrderIn(
    "ASSEMBLED",
    kitchen.restaurantId,
    kitchen.phoId,
  );

  const outcome = await processor.process(deliveryPickedUp(assembled.orderId));

  // A business outcome: acknowledged and logged, not dead-lettered.
  assert.equal(outcome, "rejected");
  assert.equal(deadLetters.length, 0);
  assert.equal(
    (await stateOf(world, assembled.orderId))?.state,
    RestaurantOrderState.Assembled,
  );
});

test("Pickup of an order this kitchen never received is acknowledged", async () => {
  const { processor, deadLetters } = await setup();

  const outcome = await processor.process(deliveryPickedUp("order-unknown"));

  assert.equal(outcome, "rejected");
  assert.equal(deadLetters.length, 0);
});

test("ADR 41 DeliveryPickedUp of another Market than the order's is dead-lettered", async () => {
  const { world, kitchen, processor, deadLetters } = await setup();
  const ready = await world.givenOrderIn(
    "READY",
    kitchen.restaurantId,
    kitchen.phoId,
  );

  const outcome = await processor.process(
    deliveryPickedUp(ready.orderId, { marketId: "hamburg" }),
  );

  assert.equal(outcome, "dead-lettered");
  assert.equal(deadLetters[0]?.topic, `${DELIVERY_EVENTS_TOPIC}.dlq`);
  assert.equal(
    (await stateOf(world, ready.orderId))?.state,
    RestaurantOrderState.Ready,
  );
});
