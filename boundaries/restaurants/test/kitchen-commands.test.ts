import assert from "node:assert/strict";
import test from "node:test";

import {
  AlreadyExistsError,
  NotFoundError,
  PermissionDeniedError,
} from "../pkg/application/errors.ts";
import type { Outbox } from "../pkg/application/outbox.ts";
import {
  AlreadyInStateError,
  GuardFailedError,
  InvalidRestaurantOrderError,
  InvalidTransitionError,
  RestaurantOrder,
} from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import {
  cook,
  createKitchenWorld,
  minutes,
  ordersService,
  PREPARATION_MINUTES,
} from "./support/kitchen.ts";
import {
  createInfrastructure,
  owner,
  stranger,
} from "./support/restaurants.ts";

const staffCommand = (restaurantId: string, restaurantOrderId: string) => ({
  subject: cook,
  restaurantId,
  restaurantOrderId,
});

test("AC-242 receive order request creates a RECEIVED restaurant order", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();

  const result = await world.receive.execute({
    subject: ordersService,
    restaurantId,
    orderId: "order-42",
    items: [{ menuItemId: phoId, quantity: 2, options: ["spicy"] }],
  });

  assert.equal(result.created, true);
  assert.deepEqual(result.restaurantOrder, {
    restaurantOrderId: "restaurant-order-1",
    orderId: "order-42",
    restaurantId,
    state: "RECEIVED",
    items: [{ menuItemId: phoId, quantity: 2, options: ["spicy"] }],
    timeline: {},
    rejectionReason: undefined,
    failureReason: undefined,
    cancelledAtStage: undefined,
    version: 1,
  });
});

test("AC-243 a repeated receive returns the existing order", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const request = {
    subject: ordersService,
    restaurantId,
    orderId: "order-42",
    items: [{ menuItemId: phoId, quantity: 2 }],
  };

  const first = await world.receive.execute(request);
  const repeated = await world.receive.execute(request);

  assert.equal(repeated.created, false);
  assert.deepEqual(repeated.restaurantOrder, first.restaurantOrder);
  assert.equal(world.orderStore.all().length, 1);
});

test("AC-244 a conflicting repeat is rejected as already exists", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  await world.receive.execute({
    subject: ordersService,
    restaurantId,
    orderId: "order-42",
    items: [{ menuItemId: phoId, quantity: 2 }],
  });

  await assert.rejects(
    world.receive.execute({
      subject: ordersService,
      restaurantId,
      orderId: "order-42",
      items: [{ menuItemId: phoId, quantity: 3 }],
    }),
    AlreadyExistsError,
  );
});

test("concurrent receive that loses the unique orderId race returns the winner", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const items = [{ menuItemId: phoId, quantity: 1 }];
  const original = world.restaurantOrders.findByOrderId.bind(
    world.restaurantOrders,
  );
  let firstLookup = true;
  world.restaurantOrders.findByOrderId = async (orderId) => {
    if (firstLookup) {
      firstLookup = false;
      const winner = RestaurantOrder.receive({
        id: "winner",
        orderId,
        restaurantId,
        items,
        receivedAt: world.clock.now(),
      });
      await world.restaurantOrders.save(winner);
      return undefined;
    }
    return original(orderId);
  };

  const result = await world.receive.execute({
    subject: ordersService,
    restaurantId,
    orderId: "order-42",
    items,
  });

  assert.equal(result.created, false);
  assert.equal(result.restaurantOrder.restaurantOrderId, "winner");
  assert.equal(world.orderStore.all().length, 1);
});

test("AC-245 invalid composition is rejected and nothing is stored", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();

  for (const items of [
    [],
    [{ menuItemId: phoId, quantity: 0 }],
    [
      { menuItemId: phoId, quantity: 1, options: ["a"] },
      { menuItemId: phoId, quantity: 1, options: ["a"] },
    ],
  ]) {
    await assert.rejects(
      world.receive.execute({
        subject: ordersService,
        restaurantId,
        orderId: "order-42",
        items,
      }),
      InvalidRestaurantOrderError,
    );
  }
  assert.deepEqual(world.orderStore.all(), []);
});

test("receive order request requires the fulfillment service identity and a known restaurant", async () => {
  const world = createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  world.kitchenAccessControl.grantFulfillment(ordersService, "missing");

  await assert.rejects(
    world.receive.execute({
      subject: cook,
      restaurantId,
      orderId: "order-42",
      items: [{ menuItemId: phoId, quantity: 1 }],
    }),
    PermissionDeniedError,
  );
  await assert.rejects(
    world.receive.execute({
      subject: ordersService,
      restaurantId: "missing",
      orderId: "order-42",
      items: [{ menuItemId: phoId, quantity: 1 }],
    }),
    NotFoundError,
  );
  assert.deepEqual(world.orderStore.all(), []);
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
