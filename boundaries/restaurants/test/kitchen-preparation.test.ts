import assert from "node:assert/strict";
import test from "node:test";

import {
  NotFoundError,
  PermissionDeniedError,
} from "../pkg/application/errors.ts";
import type { Outbox } from "../pkg/application/outbox.ts";
import {
  AlreadyInStateError,
  GuardFailedError,
  InvalidTransitionError,
} from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import {
  createKitchenWorld,
  minutes,
  ordersService,
} from "./support/kitchen.ts";
import {
  createInfrastructure,
  owner,
  stranger,
} from "./support/restaurants.ts";

test("AC-251 queue restaurant order queues an ACCEPTED order without publishing", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const received = await world.givenReceivedOrder(restaurantId, phoId);
  const order = await world.restaurantOrders.get(received.restaurantOrderId);
  assert.ok(order);
  order.accept({
    acceptedAt: world.clock.now(),
    expectedReadyAt: world.clock.now(),
    eligibility: { satisfied: true, reasons: [] },
  });
  await world.restaurantOrders.save(order);
  const outboxBefore = world.outbox.records.length;
  world.clock.advance(minutes(1));

  const result = await world.queue.execute({
    restaurantOrderId: received.restaurantOrderId,
  });

  assert.equal(result.state, "QUEUED");
  assert.deepEqual(result.timeline.queuedAt, world.clock.now());
  assert.equal(world.outbox.records.length, outboxBefore);
});

test("AC-252 queueing a queued order is a no-op", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const before = await world.restaurantOrders.get(order.restaurantOrderId);

  const result = await world.queue.execute({
    restaurantOrderId: order.restaurantOrderId,
  });

  assert.equal(result.state, "QUEUED");
  assert.equal(result.version, before?.version);
  await assert.rejects(
    world.queue.execute({ restaurantOrderId: "missing" }),
    NotFoundError,
  );
});

test("AC-253 start preparation publishes PreparationStarted", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  world.clock.advance(minutes(3));

  const result = await world.startPreparation.execute(order);

  assert.equal(result.state, "PREPARING");
  assert.deepEqual(result.timeline.preparationStartedAt, world.clock.now());
  assert.equal(world.outbox.records.at(-1)?.event.type, "PreparationStarted");
});

test("AC-254 repeated start preparation is a no-op under the no-op policy", async () => {
  const world = createKitchenWorld({ onRepeat: "succeed-without-event" });
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("PREPARING", restaurantId, phoId);
  const outboxBefore = world.outbox.records.length;
  const before = await world.restaurantOrders.get(order.restaurantOrderId);

  const result = await world.startPreparation.execute(order);

  assert.equal(result.state, "PREPARING");
  assert.equal(result.version, before?.version);
  assert.equal(world.outbox.records.length, outboxBefore);
});

test("AC-255 repeated start preparation is rejected under the strict policy", async () => {
  const world = createKitchenWorld({ onRepeat: "reject" });
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("PREPARING", restaurantId, phoId);

  await assert.rejects(
    world.startPreparation.execute(order),
    AlreadyInStateError,
  );
  await world.markAssembled.execute(order);
  await world.markReady.execute(order);
  await assert.rejects(world.markReady.execute(order), AlreadyInStateError);
});

test("AC-256 mark assembled is a local fact without Outbox", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("PREPARING", restaurantId, phoId);
  const outboxBefore = world.outbox.records.length;

  const result = await world.markAssembled.execute(order);

  assert.equal(result.state, "ASSEMBLED");
  assert.equal(world.outbox.records.length, outboxBefore);
  await assert.rejects(world.markAssembled.execute(order), AlreadyInStateError);
  await assert.rejects(
    world.markAssembled.execute({ ...order, subject: stranger }),
    PermissionDeniedError,
  );
});

test("AC-257 mark ready publishes RestaurantOrderReady", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("ASSEMBLED", restaurantId, phoId);
  world.clock.advance(minutes(15));

  const result = await world.markReady.execute(order);

  assert.equal(result.state, "READY");
  assert.deepEqual(result.timeline.readyAt, world.clock.now());
  assert.equal(world.outbox.records.at(-1)?.event.type, "RestaurantOrderReady");
  const repeated = await world.markReady.execute(order);
  assert.equal(repeated.version, result.version);
});

test("AC-258 an order cannot become ready before Assembly", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("PREPARING", restaurantId, phoId);

  await assert.rejects(world.markReady.execute(order), InvalidTransitionError);
  assert.equal(
    (await world.restaurantOrders.get(order.restaurantOrderId))?.state,
    "PREPARING",
  );
});

test("AC-259 report delay moves expected_ready_at and publishes PreparationDelayed", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("PREPARING", restaurantId, phoId);
  const before = await world.restaurantOrders.get(order.restaurantOrderId);
  const previous = before?.timeline.expectedReadyAt;
  assert.ok(previous);
  const later = new Date(previous.getTime() + minutes(10));

  const result = await world.reportDelay.execute({
    ...order,
    expectedReadyAt: later,
    reason: "oven",
  });

  assert.equal(result.state, "PREPARING");
  assert.deepEqual(result.timeline.expectedReadyAt, later);
  const event = world.outbox.records.at(-1)?.event;
  assert.ok(
    event?.type === "PreparationDelayed" &&
      "previousExpectedReadyAt" in event &&
      (event.previousExpectedReadyAt as Date).getTime() === previous.getTime(),
  );
});

test("AC-260 report delay rejects a time that is not later", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const before = await world.restaurantOrders.get(order.restaurantOrderId);
  const current = before?.timeline.expectedReadyAt;
  assert.ok(current);
  const outboxBefore = world.outbox.records.length;

  for (const expectedReadyAt of [current, new Date(current.getTime() - 1)]) {
    await assert.rejects(
      world.reportDelay.execute({ ...order, expectedReadyAt }),
      (error: unknown) =>
        error instanceof GuardFailedError &&
        error.reasons[0]?.code === "not_later",
    );
  }
  const after = await world.restaurantOrders.get(order.restaurantOrderId);
  assert.equal(after?.version, before?.version);
  assert.equal(world.outbox.records.length, outboxBefore);
});

test("AC-261 report failure stores the reason and publishes the failure fact", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("ASSEMBLED", restaurantId, phoId);

  const result = await world.reportFailure.execute({
    ...order,
    reason: "ITEM_UNAVAILABLE",
  });

  assert.equal(result.state, "FAILED");
  assert.equal(result.failureReason, "ITEM_UNAVAILABLE");
  assert.equal(
    world.outbox.records.at(-1)?.event.type,
    "RestaurantOrderFailed",
  );
});

test("AC-262 failure before Preparation is an invalid transition", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);

  await assert.rejects(
    world.reportFailure.execute({ ...order, reason: "ITEM_UNAVAILABLE" }),
    InvalidTransitionError,
  );
});

test("published kitchen fact is rolled back with the state when the outbox fails", async () => {
  const failingOutbox: Outbox = {
    add: async (aggregate, events) => {
      if (events.some((event) => event.type === "PreparationStarted")) {
        throw new Error("outbox unavailable");
      }
      return world.outbox.add(aggregate, events);
    },
  };
  const infrastructure = createInfrastructure();
  const world = createKitchenWorld({ infrastructure, outbox: failingOutbox });
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);

  await assert.rejects(
    world.startPreparation.execute(order),
    /outbox unavailable/,
  );
  assert.equal(
    (await world.restaurantOrders.get(order.restaurantOrderId))?.state,
    "QUEUED",
  );
});

test("AC-268 a cancelled order cannot start preparation", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  await world.cancel.execute({
    subject: ordersService,
    restaurantId,
    orderId: order.orderId,
  });

  await assert.rejects(
    world.startPreparation.execute(order),
    InvalidTransitionError,
  );
  assert.equal(
    (await world.restaurantOrders.get(order.restaurantOrderId))?.state,
    "CANCELLED",
  );
});

test("AC-208 closing the restaurant does not change accepted orders", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);

  await world.close.execute({ subject: owner, restaurantId });

  assert.equal(world.outbox.records.at(-1)?.event.type, "RestaurantClosed");
  assert.equal(
    (await world.restaurantOrders.get(order.restaurantOrderId))?.state,
    "QUEUED",
  );
});
