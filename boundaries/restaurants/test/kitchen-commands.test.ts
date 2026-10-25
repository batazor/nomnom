import assert from "node:assert/strict";
import test from "node:test";

import {
  NotFoundError,
  PermissionDeniedError,
} from "../pkg/application/errors.ts";
import {
  GuardFailedError,
  InvalidTransitionError,
} from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import {
  cook,
  createKitchenWorld,
  minutes,
  PREPARATION_MINUTES,
} from "./support/kitchen.ts";
import { owner, stranger } from "./support/restaurants.ts";

const staffCommand = (restaurantId: string, restaurantOrderId: string) => ({
  subject: cook,
  restaurantId,
  restaurantOrderId,
});

test("AC-246 accept queues the order and publishes only RestaurantOrderAccepted", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const received = await world.givenReceivedOrder(restaurantId, phoId);
  const outboxBefore = world.outbox.records.length;
  const now = world.clock.now();

  const result = await world.accept.execute(
    staffCommand(restaurantId, received.restaurantOrderId),
  );

  const expectedReadyAt = new Date(
    now.getTime() + minutes(PREPARATION_MINUTES),
  );
  assert.equal(result.state, "QUEUED");
  assert.deepEqual(result.timeline, {
    acceptedAt: now,
    queuedAt: now,
    expectedReadyAt,
  });
  assert.equal(result.version, 2);
  assert.deepEqual(world.outbox.records.slice(outboxBefore), [
    {
      aggregate: {
        type: "RestaurantOrder",
        id: received.restaurantOrderId,
        version: 2,
      },
      event: {
        type: "RestaurantOrderAccepted",
        acceptedAt: now,
        expectedReadyAt,
        restaurantOrderId: received.restaurantOrderId,
        orderId: received.orderId,
        restaurantId,
        occurredAt: now,
      },
    },
  ]);
});

test("AC-247 accept is rejected with reasons when the restaurant is closed or items unavailable", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const received = await world.givenReceivedOrder(restaurantId, phoId);
  await world.close.execute({ subject: owner, restaurantId });
  await world.setAvailability.execute({
    subject: owner,
    restaurantId,
    menuItemId: phoId,
    isAvailable: false,
  });
  world.conditions.restaurant = {
    ...world.conditions.restaurant,
    currentOrders: 5,
    maximumOrders: 5,
  };
  const outboxBefore = world.outbox.records.length;

  await assert.rejects(
    world.accept.execute(
      staffCommand(restaurantId, received.restaurantOrderId),
    ),
    (error: unknown) =>
      error instanceof GuardFailedError &&
      JSON.stringify(error.reasons) ===
        JSON.stringify([
          { code: "restaurant_closed" },
          { code: "capacity_exhausted" },
          { code: "temporarily_unavailable", menuItemId: phoId },
        ]),
  );
  const order = await world.restaurantOrders.get(received.restaurantOrderId);
  assert.equal(order?.state, "RECEIVED");
  assert.equal(world.outbox.records.length, outboxBefore);
});

test("accept reports an item missing from the menu", async () => {
  const world = createKitchenWorld();
  const { restaurantId } = await world.givenKitchen();
  const received = await world.givenReceivedOrder(restaurantId, "ghost-item");

  await assert.rejects(
    world.accept.execute(
      staffCommand(restaurantId, received.restaurantOrderId),
    ),
    (error: unknown) =>
      error instanceof GuardFailedError &&
      error.reasons.some(
        (reason) =>
          reason.code === "menu_item_not_found" &&
          reason.menuItemId === "ghost-item",
      ),
  );
});

test("AC-248 accepting an already queued order is an invalid transition", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const outboxBefore = world.outbox.records.length;

  await assert.rejects(world.accept.execute(order), InvalidTransitionError);
  assert.equal(world.outbox.records.length, outboxBefore);
});

test("AC-240 staff of another restaurant cannot accept the order", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const received = await world.givenReceivedOrder(restaurantId, phoId);

  await assert.rejects(
    world.accept.execute({
      subject: stranger,
      restaurantId,
      restaurantOrderId: received.restaurantOrderId,
    }),
    PermissionDeniedError,
  );
  assert.equal(
    (await world.restaurantOrders.get(received.restaurantOrderId))?.state,
    "RECEIVED",
  );
});

test("AC-241 an order of another restaurant is not disclosed", async () => {
  const world = createKitchenWorld();
  const a = await world.givenKitchen("A");
  const b = await world.givenKitchen("B");
  const orderOfB = await world.givenReceivedOrder(b.restaurantId, b.phoId);

  await assert.rejects(
    world.accept.execute(
      staffCommand(a.restaurantId, orderOfB.restaurantOrderId),
    ),
    NotFoundError,
  );
  await assert.rejects(
    world.getRestaurantOrder.execute(
      staffCommand(a.restaurantId, orderOfB.restaurantOrderId),
    ),
    NotFoundError,
  );
  assert.equal(
    (await world.restaurantOrders.get(orderOfB.restaurantOrderId))?.state,
    "RECEIVED",
  );
});

test("kitchen command fails closed when authorization is unavailable", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const received = await world.givenReceivedOrder(restaurantId, phoId);
  world.kitchenAccessControl.canOperateKitchen = async () => {
    throw new Error("authorization unavailable");
  };

  await assert.rejects(
    world.reject.execute(
      staffCommand(restaurantId, received.restaurantOrderId),
    ),
    /authorization unavailable/,
  );
  assert.equal(
    (await world.restaurantOrders.get(received.restaurantOrderId))?.state,
    "RECEIVED",
  );
});

test("AC-249 reject stores the reason and publishes RestaurantOrderRejected", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const received = await world.givenReceivedOrder(restaurantId, phoId);

  const result = await world.reject.execute({
    ...staffCommand(restaurantId, received.restaurantOrderId),
    reason: "kitchen overloaded",
  });

  assert.equal(result.state, "REJECTED");
  assert.equal(result.rejectionReason, "kitchen overloaded");
  const last = world.outbox.records.at(-1)?.event;
  assert.ok(
    last?.type === "RestaurantOrderRejected" &&
      "stage" in last &&
      last.stage === "RECEIVED" &&
      "reason" in last &&
      last.reason === "kitchen overloaded",
  );
});

test("AC-250 rejection after Acceptance is an invalid transition", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);

  await assert.rejects(world.reject.execute(order), InvalidTransitionError);
  assert.equal(
    (await world.restaurantOrders.get(order.restaurantOrderId))?.state,
    "QUEUED",
  );
});
