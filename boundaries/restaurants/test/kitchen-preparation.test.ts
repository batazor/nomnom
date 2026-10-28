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
  InvalidRestaurantOrderError,
  InvalidTransitionError,
} from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import {
  cook,
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
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const received = await world.givenReceivedOrder(restaurantId, phoId);
  const order = await world.restaurantOrders.get(received.restaurantOrderId);
  assert.ok(order);
  order.accept({
    acceptedAt: world.clock.now(),
    expectedReadyAt: new Date(world.clock.now().getTime() + minutes(20)),
    eligibility: { satisfied: true, reasons: [] },
  });
  await world.restaurantOrders.save(order);
  const outboxBefore = (await world.outbox.records()).length;
  world.clock.advance(minutes(1));

  const result = await world.queue.execute({
    subject: ordersService,
    restaurantOrderId: received.restaurantOrderId,
  });

  assert.equal(result.state, "QUEUED");
  assert.deepEqual(result.timeline.queuedAt, world.clock.now());
  assert.equal((await world.outbox.records()).length, outboxBefore);
});

test("AC-252 queueing a queued order is a no-op", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const before = await world.restaurantOrders.get(order.restaurantOrderId);

  const result = await world.queue.execute({
    subject: ordersService,
    restaurantOrderId: order.restaurantOrderId,
  });

  assert.equal(result.state, "QUEUED");
  assert.equal(result.version, before?.version);
  await assert.rejects(
    world.queue.execute({
      subject: ordersService,
      restaurantOrderId: "missing",
    }),
    NotFoundError,
  );
});

test("AC-253 start preparation publishes PreparationStarted", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  world.clock.advance(minutes(3));

  const result = await world.startNext(restaurantId);

  assert.equal(result?.restaurantOrderId, order.restaurantOrderId);
  assert.equal(result?.state, "PREPARING");
  assert.deepEqual(result?.timeline.preparationStartedAt, world.clock.now());
  assert.equal(
    (await world.outbox.records()).at(-1)?.event.type,
    "PreparationStarted",
  );
});

test("AC-860 start next takes the premium order before an earlier standard one", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const standard = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  world.clock.advance(minutes(5));
  const premium = await world.givenReceivedOrder(
    restaurantId,
    phoId,
    "PREMIUM",
  );
  await world.accept.execute({
    subject: cook,
    restaurantId,
    restaurantOrderId: premium.restaurantOrderId,
  });

  const first = await world.startNext(restaurantId);
  const second = await world.startNext(restaurantId);

  assert.deepEqual(
    [first?.restaurantOrderId, first?.priority],
    [premium.restaurantOrderId, "PREMIUM"],
  );
  assert.equal(second?.restaurantOrderId, standard.restaurantOrderId);
});

test("AC-861 start next on an empty queue starts nothing and publishes nothing", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  await world.givenOrderIn("RECEIVED", restaurantId, phoId);
  const outboxBefore = (await world.outbox.records()).length;

  assert.equal(await world.startNext(restaurantId), undefined);
  assert.equal((await world.outbox.records()).length, outboxBefore);
});

test("FR-225 start next requires a request id", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  await world.givenOrderIn("QUEUED", restaurantId, phoId);

  await assert.rejects(
    world.startNext(restaurantId, " "),
    InvalidRestaurantOrderError,
  );
});

test("AC-254 a repeat with the same request_id starts no other order under the no-op policy", async () => {
  const world = await createKitchenWorld({ onRepeat: "succeed-without-event" });
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const next = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const first = await world.startNext(restaurantId, "request-1");
  const outboxBefore = (await world.outbox.records()).length;

  const result = await world.startNext(restaurantId, "request-1");

  assert.equal(first?.restaurantOrderId, order.restaurantOrderId);
  assert.equal(result?.restaurantOrderId, order.restaurantOrderId);
  assert.equal(result?.state, "PREPARING");
  assert.equal(result?.version, first?.version);
  assert.equal((await world.outbox.records()).length, outboxBefore);
  assert.equal(
    (await world.restaurantOrders.get(next.restaurantOrderId))?.state,
    "QUEUED",
  );
});

test("AC-255 a repeat with the same request_id is rejected under the strict policy", async () => {
  const world = await createKitchenWorld({ onRepeat: "reject" });
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const next = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  await world.startNext(restaurantId, "request-1");

  await assert.rejects(
    world.startNext(restaurantId, "request-1"),
    AlreadyInStateError,
  );
  assert.equal(
    (await world.restaurantOrders.get(next.restaurantOrderId))?.state,
    "QUEUED",
  );
  await world.markAssembled.execute(order);
  await world.markReady.execute(order);
  await assert.rejects(world.markReady.execute(order), AlreadyInStateError);
});

test("AC-256 mark assembled is a local fact without Outbox", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("PREPARING", restaurantId, phoId);
  const outboxBefore = (await world.outbox.records()).length;

  const result = await world.markAssembled.execute(order);

  assert.equal(result.state, "ASSEMBLED");
  assert.equal((await world.outbox.records()).length, outboxBefore);
  await assert.rejects(world.markAssembled.execute(order), AlreadyInStateError);
  await assert.rejects(
    world.markAssembled.execute({ ...order, subject: stranger }),
    PermissionDeniedError,
  );
});

test("AC-257 mark ready publishes RestaurantOrderReady", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("ASSEMBLED", restaurantId, phoId);
  world.clock.advance(minutes(15));

  const result = await world.markReady.execute(order);

  assert.equal(result.state, "READY");
  assert.deepEqual(result.timeline.readyAt, world.clock.now());
  assert.equal(
    (await world.outbox.records()).at(-1)?.event.type,
    "RestaurantOrderReady",
  );
  const repeated = await world.markReady.execute(order);
  assert.equal(repeated.version, result.version);
});

test("AC-258 an order cannot become ready before Assembly", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("PREPARING", restaurantId, phoId);

  await assert.rejects(world.markReady.execute(order), InvalidTransitionError);
  assert.equal(
    (await world.restaurantOrders.get(order.restaurantOrderId))?.state,
    "PREPARING",
  );
});

test("AC-259 report delay moves expected_ready_at and publishes PreparationDelayed", async () => {
  const world = await createKitchenWorld();
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
  const event = (await world.outbox.records()).at(-1)?.event;
  assert.ok(
    event?.type === "PreparationDelayed" &&
      "previousExpectedReadyAt" in event &&
      (event.previousExpectedReadyAt as Date).getTime() === previous.getTime(),
  );
});

test("AC-260 report delay rejects a time that is not later", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const before = await world.restaurantOrders.get(order.restaurantOrderId);
  const current = before?.timeline.expectedReadyAt;
  assert.ok(current);
  const outboxBefore = (await world.outbox.records()).length;

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
  assert.equal((await world.outbox.records()).length, outboxBefore);
});

test("AC-261 report failure stores the reason and publishes the failure fact", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("ASSEMBLED", restaurantId, phoId);

  const result = await world.reportFailure.execute({
    ...order,
    reason: "ITEM_UNAVAILABLE",
  });

  assert.equal(result.state, "FAILED");
  assert.equal(result.failureReason, "ITEM_UNAVAILABLE");
  assert.equal(
    (await world.outbox.records()).at(-1)?.event.type,
    "RestaurantOrderFailed",
  );
});

test("AC-262 failure before Preparation is an invalid transition", async () => {
  const world = await createKitchenWorld();
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
  const infrastructure = await createInfrastructure();
  const world = await createKitchenWorld({
    infrastructure,
    outbox: failingOutbox,
  });
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);

  await assert.rejects(world.startNext(restaurantId), /outbox unavailable/);
  assert.equal(
    (await world.restaurantOrders.get(order.restaurantOrderId))?.state,
    "QUEUED",
  );
});

test("AC-268 AC-861 a cancelled order leaves the queue: start next starts nothing", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  await world.cancel.execute({
    subject: ordersService,
    restaurantId,
    orderId: order.orderId,
  });
  const outboxBefore = (await world.outbox.records()).length;

  assert.equal(await world.startNext(restaurantId), undefined);
  assert.equal((await world.outbox.records()).length, outboxBefore);
  assert.equal(
    (await world.restaurantOrders.get(order.restaurantOrderId))?.state,
    "CANCELLED",
  );
});

test("AC-208 closing the restaurant does not change accepted orders", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);

  await world.close.execute({ subject: owner, restaurantId });

  assert.equal(
    (await world.outbox.records()).at(-1)?.event.type,
    "RestaurantClosed",
  );
  assert.equal(
    (await world.restaurantOrders.get(order.restaurantOrderId))?.state,
    "QUEUED",
  );
});
