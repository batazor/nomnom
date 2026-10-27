import assert from "node:assert/strict";
import test from "node:test";

import {
  NotFoundError,
  PermissionDeniedError,
} from "../pkg/application/errors.ts";
import { GetKitchenQueueHandler } from "../slices/restaurantorder/applications/get-kitchen-queue/get-kitchen-queue.ts";
import {
  cook,
  createKitchenWorld,
  minutes,
  ordersService,
} from "./support/kitchen.ts";
import { stranger } from "./support/restaurants.ts";

test("AC-263 cancel before Preparation cancels and publishes RestaurantOrderCancelled", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);

  const result = await world.cancel.execute({
    subject: ordersService,
    restaurantId,
    orderId: order.orderId,
    reason: "acceptance timeout",
  });

  assert.deepEqual(result, {
    outcome: "cancelled",
    restaurantOrderId: order.restaurantOrderId,
    stage: "QUEUED",
    repeated: false,
  });
  const event = (await world.outbox.records()).at(-1)?.event;
  assert.ok(
    event?.type === "RestaurantOrderCancelled" &&
      "stage" in event &&
      event.stage === "QUEUED" &&
      "preparationStarted" in event &&
      event.preparationStarted === false,
  );
  assert.equal(
    (await world.restaurantOrders.get(order.restaurantOrderId))?.state,
    "CANCELLED",
  );
});

test("cancel of a received order uses the RECEIVED stage", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("RECEIVED", restaurantId, phoId);

  const result = await world.cancel.execute({
    subject: ordersService,
    restaurantId,
    orderId: order.orderId,
  });

  assert.ok(result.outcome === "cancelled" && result.stage === "RECEIVED");
});

test("AC-264 repeated cancel returns the original stage without a new fact", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const command = {
    subject: ordersService,
    restaurantId,
    orderId: order.orderId,
  };
  await world.cancel.execute(command);
  const outboxBefore = (await world.outbox.records()).length;

  const repeated = await world.cancel.execute(command);

  assert.deepEqual(repeated, {
    outcome: "cancelled",
    restaurantOrderId: order.restaurantOrderId,
    stage: "QUEUED",
    repeated: true,
  });
  assert.equal((await world.outbox.records()).length, outboxBefore);
});

test("AC-265 cancel of a ready order is rejected as already ready", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("READY", restaurantId, phoId);
  const outboxBefore = (await world.outbox.records()).length;

  const result = await world.cancel.execute({
    subject: ordersService,
    restaurantId,
    orderId: order.orderId,
  });

  assert.deepEqual(result, {
    outcome: "cancellation_rejected",
    restaurantOrderId: order.restaurantOrderId,
    reason: "already_ready",
    state: "READY",
  });
  assert.equal((await world.outbox.records()).length, outboxBefore);
  assert.equal(
    (await world.restaurantOrders.get(order.restaurantOrderId))?.state,
    "READY",
  );
});

test("cancel of rejected and failed orders is rejected with the terminal reason", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const rejected = await world.givenOrderIn("RECEIVED", restaurantId, phoId);
  await world.reject.execute(rejected);
  const failed = await world.givenOrderIn("PREPARING", restaurantId, phoId);
  await world.reportFailure.execute({ ...failed, reason: "burnt" });

  const rejectedResult = await world.cancel.execute({
    subject: ordersService,
    restaurantId,
    orderId: rejected.orderId,
  });
  const failedResult = await world.cancel.execute({
    subject: ordersService,
    restaurantId,
    orderId: failed.orderId,
  });

  assert.ok(
    rejectedResult.outcome === "cancellation_rejected" &&
      rejectedResult.reason === "already_rejected",
  );
  assert.ok(
    failedResult.outcome === "cancellation_rejected" &&
      failedResult.reason === "already_failed",
  );
});

test("AC-266 cancel during Preparation is rejected by the reject policy", async () => {
  const world = await createKitchenWorld({
    cancellationDuringPreparation: "reject",
  });
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("PREPARING", restaurantId, phoId);

  const result = await world.cancel.execute({
    subject: ordersService,
    restaurantId,
    orderId: order.orderId,
  });

  assert.deepEqual(result, {
    outcome: "cancellation_rejected",
    restaurantOrderId: order.restaurantOrderId,
    reason: "cancellation_during_preparation_not_allowed",
    state: "PREPARING",
  });
  assert.equal(
    (await world.restaurantOrders.get(order.restaurantOrderId))?.state,
    "PREPARING",
  );
});

test("AC-267 cancel during Preparation is applied by the allow policy", async () => {
  const world = await createKitchenWorld({
    cancellationDuringPreparation: "allow",
  });
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("ASSEMBLED", restaurantId, phoId);

  const result = await world.cancel.execute({
    subject: ordersService,
    restaurantId,
    orderId: order.orderId,
  });

  assert.ok(result.outcome === "cancelled" && result.stage === "ASSEMBLED");
  const event = (await world.outbox.records()).at(-1)?.event;
  assert.ok(
    event?.type === "RestaurantOrderCancelled" &&
      "preparationStarted" in event &&
      event.preparationStarted === true,
  );
});

test("cancel requires the fulfillment identity and a known order of the restaurant", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);

  await assert.rejects(
    world.cancel.execute({
      subject: cook,
      restaurantId,
      orderId: order.orderId,
    }),
    PermissionDeniedError,
  );
  await assert.rejects(
    world.cancel.execute({
      subject: ordersService,
      restaurantId,
      orderId: "unknown-order",
    }),
    NotFoundError,
  );
  assert.equal(
    (await world.restaurantOrders.get(order.restaurantOrderId))?.state,
    "QUEUED",
  );
});

test("AC-269 get restaurant order returns composition, state and timeline", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const now = world.clock.now();

  const view = await world.getRestaurantOrder.execute(order);
  const viewByService = await world.getRestaurantOrder.execute({
    ...order,
    subject: ordersService,
  });

  assert.equal(view.state, "QUEUED");
  assert.deepEqual(view.items, [
    { menuItemId: phoId, quantity: 2, options: [] },
  ]);
  assert.deepEqual(view.timeline, {
    receivedAt: now,
    acceptedAt: now,
    queuedAt: now,
    expectedReadyAt: new Date(now.getTime() + minutes(20)),
  });
  assert.deepEqual(viewByService, view);
  await assert.rejects(
    world.getRestaurantOrder.execute({ ...order, subject: stranger }),
    PermissionDeniedError,
  );
});

test("AC-280 kitchen board requires restaurant staff", async () => {
  const world = await createKitchenWorld();
  const { restaurantId } = await world.givenKitchen();

  for (const query of [
    world.getKitchenQueue,
    world.listActivePreparations,
    world.listReadyOrders,
  ]) {
    await assert.rejects(
      query.execute({ subject: stranger, restaurantId }),
      PermissionDeniedError,
    );
  }
});

test("AC-281 kitchen queue lists queued orders in the policy order", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const other = await world.givenKitchen("Other");
  await world.givenOrderIn("RECEIVED", restaurantId, phoId);
  await world.givenOrderIn("PREPARING", restaurantId, phoId);
  const first = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  world.clock.advance(minutes(1));
  const second = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  await world.givenOrderIn("QUEUED", other.restaurantId, other.phoId);

  const queue = await world.getKitchenQueue.execute({
    subject: cook,
    restaurantId,
  });
  const reversed = await new GetKitchenQueueHandler({
    accessControl: world.kitchenAccessControl,
    reader: world.board,
    ordering: { order: async (_, entries) => [...entries].reverse() },
    clock: world.clock,
  }).execute({ subject: cook, restaurantId });

  assert.deepEqual(
    queue.entries.map((entry) => entry.restaurantOrderId),
    [first.restaurantOrderId, second.restaurantOrderId],
  );
  assert.ok(
    queue.entries.every((entry) => entry.expectedReadyAt !== undefined),
  );
  assert.deepEqual(
    reversed.entries.map((entry) => entry.restaurantOrderId),
    [second.restaurantOrderId, first.restaurantOrderId],
  );
});

test("AC-860 AC-862 premium orders come first, a long waiting standard order joins them", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const aged = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  world.clock.advance(minutes(5));
  const standard = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const received = await world.givenReceivedOrder(
    restaurantId,
    phoId,
    "PREMIUM",
  );
  await world.accept.execute({
    subject: cook,
    restaurantId,
    restaurantOrderId: received.restaurantOrderId,
  });
  const queueOrder = async () =>
    (
      await world.getKitchenQueue.execute({ subject: cook, restaurantId })
    ).entries.map((entry) => [entry.restaurantOrderId, entry.priority]);

  // Baseline 20 minutes, aging factor 2: nobody waits longer than 40.
  assert.deepEqual(await queueOrder(), [
    [received.restaurantOrderId, "PREMIUM"],
    [aged.restaurantOrderId, "STANDARD"],
    [standard.restaurantOrderId, "STANDARD"],
  ]);

  // The first standard order waits 45 minutes: it goes before the premium one.
  world.clock.advance(minutes(40));
  assert.deepEqual(await queueOrder(), [
    [aged.restaurantOrderId, "STANDARD"],
    [received.restaurantOrderId, "PREMIUM"],
    [standard.restaurantOrderId, "STANDARD"],
  ]);
});

test("AC-282 active preparations list preparing and assembled orders with delays", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const preparing = await world.givenOrderIn("PREPARING", restaurantId, phoId);
  const assembled = await world.givenOrderIn("ASSEMBLED", restaurantId, phoId);
  await world.givenOrderIn("READY", restaurantId, phoId);
  await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const delayedUntil = new Date(world.clock.now().getTime() + minutes(45));
  await world.reportDelay.execute({
    ...preparing,
    expectedReadyAt: delayedUntil,
  });

  const result = await world.listActivePreparations.execute({
    subject: cook,
    restaurantId,
  });

  assert.deepEqual(
    result.preparations.map((entry) => [entry.restaurantOrderId, entry.state]),
    [
      [preparing.restaurantOrderId, "PREPARING"],
      [assembled.restaurantOrderId, "ASSEMBLED"],
    ],
  );
  assert.deepEqual(result.preparations[0]?.expectedReadyAt, delayedUntil);
});

test("AC-283 ready orders list only READY orders with ready_at", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  await world.givenOrderIn("PREPARING", restaurantId, phoId);
  const ready = await world.givenOrderIn("READY", restaurantId, phoId);

  const result = await world.listReadyOrders.execute({
    subject: cook,
    restaurantId,
  });

  assert.deepEqual(result.orders, [
    {
      restaurantOrderId: ready.restaurantOrderId,
      orderId: ready.orderId,
      readyAt: world.clock.now(),
    },
  ]);
});
