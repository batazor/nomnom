import assert from "node:assert/strict";
import test from "node:test";

import {
  type AcceptanceConditions,
  AcceptanceReasonCode,
  evaluateOrderAcceptance,
} from "../slices/restaurantorder/domains/restaurantorder/acceptance.ts";
import {
  AlreadyInStateError,
  GuardFailedError,
  InvalidRestaurantOrderError,
  InvalidRestaurantOrderReason,
  InvalidTransitionError,
  RestaurantOrder,
} from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";

const at = (minute: number): Date =>
  new Date(Date.UTC(2026, 9, 25, 12, minute));
const accepted = { satisfied: true, reasons: [] };

const receive = (items = [{ menuItemId: "pho", quantity: 2 }]) =>
  RestaurantOrder.receive({
    id: "ro-1",
    orderId: "order-1",
    restaurantId: "r-1",
    items,
    receivedAt: at(0),
  });

const types = (order: RestaurantOrder) =>
  order.pullEvents().map((event) => event.type);

test("received order is RECEIVED with a local Received fact", () => {
  const order = receive();

  assert.equal(order.state, "RECEIVED");
  assert.equal(order.items[0]?.quantity.value, 2);
  assert.deepEqual(types(order), ["RestaurantOrderReceived"]);
});

test("composition must be non-empty, positive and without duplicate options", () => {
  const cases: Array<[Parameters<typeof receive>[0], string]> = [
    [[], InvalidRestaurantOrderReason.EmptyItems],
    [
      [{ menuItemId: "pho", quantity: 0 }],
      InvalidRestaurantOrderReason.InvalidQuantity,
    ],
    [
      [{ menuItemId: "pho", quantity: 1.5 }],
      InvalidRestaurantOrderReason.InvalidQuantity,
    ],
    [
      [{ menuItemId: " ", quantity: 1 }],
      InvalidRestaurantOrderReason.EmptyMenuItemId,
    ],
    [
      [
        { menuItemId: "pho", quantity: 1, options: ["b", "a"] },
        { menuItemId: "pho", quantity: 2, options: ["a", "b"] },
      ] as never,
      InvalidRestaurantOrderReason.DuplicateItem,
    ],
  ];
  for (const [items, reason] of cases) {
    assert.throws(
      () => receive(items),
      (error: unknown) =>
        error instanceof InvalidRestaurantOrderError && error.reason === reason,
      reason,
    );
  }
  const sameItemOtherOptions = receive([
    { menuItemId: "pho", quantity: 1 },
    { menuItemId: "pho", quantity: 1, options: ["spicy"] },
  ] as never);
  assert.equal(sameItemOtherOptions.items.length, 2);
});

test("matchesRequest compares restaurant and normalized composition", () => {
  const order = receive([
    { menuItemId: "pho", quantity: 1, options: ["b", "a"] },
    { menuItemId: "tea", quantity: 2 },
  ] as never);

  assert.ok(
    order.matchesRequest({
      restaurantId: "r-1",
      items: [
        { menuItemId: "tea", quantity: 2 },
        { menuItemId: "pho", quantity: 1, options: ["a", "b"] },
      ],
    }),
  );
  assert.ok(
    !order.matchesRequest({
      restaurantId: "r-1",
      items: [{ menuItemId: "tea", quantity: 2 }],
    }),
  );
  assert.ok(
    !order.matchesRequest({
      restaurantId: "r-2",
      items: [
        { menuItemId: "tea", quantity: 2 },
        { menuItemId: "pho", quantity: 1, options: ["a", "b"] },
      ],
    }),
  );
});

test("happy path fills the timeline and records each fact", () => {
  const order = receive();
  order.pullEvents();

  order.accept({
    acceptedAt: at(1),
    expectedReadyAt: at(30),
    eligibility: accepted,
  });
  order.queue({ queuedAt: at(1) });
  order.startPreparation({ startedAt: at(5) });
  order.reportDelay({
    reportedAt: at(10),
    expectedReadyAt: at(40),
    reason: " oven ",
  });
  order.markAssembled({ assembledAt: at(35) });
  order.markReady({ readyAt: at(36) });

  assert.equal(order.state, "READY");
  assert.deepEqual(order.timeline, {
    receivedAt: at(0),
    acceptedAt: at(1),
    queuedAt: at(1),
    preparationStartedAt: at(5),
    expectedReadyAt: at(40),
    readyAt: at(36),
  });
  const events = order.pullEvents();
  assert.deepEqual(
    events.map((event) => event.type),
    [
      "RestaurantOrderAccepted",
      "RestaurantOrderQueued",
      "PreparationStarted",
      "PreparationDelayed",
      "RestaurantOrderAssembled",
      "RestaurantOrderReady",
    ],
  );
  assert.deepEqual(events[3], {
    type: "PreparationDelayed",
    expectedReadyAt: at(40),
    previousExpectedReadyAt: at(30),
    reason: "oven",
    restaurantOrderId: "ro-1",
    orderId: "order-1",
    restaurantId: "r-1",
    occurredAt: at(10),
  });
});

test("failed transition does not change state or record events", () => {
  const order = receive();
  order.pullEvents();

  assert.throws(
    () =>
      order.accept({
        acceptedAt: at(1),
        expectedReadyAt: at(30),
        eligibility: {
          satisfied: false,
          reasons: [{ code: "restaurant_closed" }],
        },
      }),
    GuardFailedError,
  );
  assert.throws(
    () => order.markReady({ readyAt: at(2) }),
    InvalidTransitionError,
  );
  assert.equal(order.state, "RECEIVED");
  assert.deepEqual(order.timeline, { receivedAt: at(0) });
  assert.deepEqual(order.pullEvents(), []);
});

test("reject records the stage and optional reason", () => {
  const order = receive();
  order.pullEvents();

  order.reject({ rejectedAt: at(1), reason: "  " });

  assert.equal(order.state, "REJECTED");
  assert.equal(order.rejectionReason, undefined);
  const [event] = order.pullEvents();
  assert.equal(event?.type, "RestaurantOrderRejected");
  assert.equal(
    event?.type === "RestaurantOrderRejected" && event.stage,
    "RECEIVED",
  );
  assert.throws(() => order.reject({ rejectedAt: at(2) }), AlreadyInStateError);
});

test("failure requires a reason and records the stage", () => {
  const order = receive();
  order.accept({
    acceptedAt: at(1),
    expectedReadyAt: at(30),
    eligibility: accepted,
  });
  order.queue({ queuedAt: at(1) });
  order.startPreparation({ startedAt: at(2) });
  order.pullEvents();

  assert.throws(
    () => order.reportFailure({ failedAt: at(3), reason: " " }),
    InvalidRestaurantOrderError,
  );
  order.reportFailure({ failedAt: at(3), reason: "ITEM_UNAVAILABLE" });

  assert.equal(order.state, "FAILED");
  assert.equal(order.failureReason, "ITEM_UNAVAILABLE");
  const [event] = order.pullEvents();
  assert.ok(
    event?.type === "RestaurantOrderFailed" && event.stage === "PREPARING",
  );
});

test("cancel stores the stage and whether preparation started", () => {
  const order = receive();
  order.accept({
    acceptedAt: at(1),
    expectedReadyAt: at(30),
    eligibility: accepted,
  });
  order.queue({ queuedAt: at(1) });
  order.startPreparation({ startedAt: at(2) });
  order.markAssembled({ assembledAt: at(3) });
  order.pullEvents();

  order.cancel({ cancelledAt: at(4), policy: { duringPreparation: "allow" } });

  assert.equal(order.state, "CANCELLED");
  assert.equal(order.cancelledAtStage, "ASSEMBLED");
  const [event] = order.pullEvents();
  assert.ok(
    event?.type === "RestaurantOrderCancelled" &&
      event.stage === "ASSEMBLED" &&
      event.preparationStarted,
  );
});

test("cancel decides the outcome by stage without throwing", () => {
  const order = receive();
  order.accept({
    acceptedAt: at(1),
    expectedReadyAt: at(30),
    eligibility: accepted,
  });
  order.queue({ queuedAt: at(1) });
  order.startPreparation({ startedAt: at(2) });
  const policy = { duringPreparation: "reject" } as const;

  assert.deepEqual(order.cancel({ cancelledAt: at(3), policy }), {
    outcome: "rejected",
    reason: "cancellation_during_preparation_not_allowed",
    state: "PREPARING",
  });
  order.markAssembled({ assembledAt: at(4) });
  order.markReady({ readyAt: at(5) });
  order.pullEvents();

  // AC-637: a Ready order is rejected with `already_ready`.
  assert.deepEqual(order.cancel({ cancelledAt: at(6), policy }), {
    outcome: "rejected",
    reason: "already_ready",
    state: "READY",
  });
  assert.equal(order.state, "READY");
  assert.deepEqual(order.pullEvents(), []);

  const queued = receive();
  assert.deepEqual(queued.cancel({ cancelledAt: at(1), policy }), {
    outcome: "cancelled",
    stage: "RECEIVED",
  });
  assert.deepEqual(queued.cancel({ cancelledAt: at(2), policy }), {
    outcome: "repeated",
    stage: "RECEIVED",
  });
});

test("snapshot round trip restores the aggregate without events", () => {
  const order = receive();
  order.accept({
    acceptedAt: at(1),
    expectedReadyAt: at(30),
    eligibility: accepted,
  });
  order.queue({ queuedAt: at(1) });
  order.cancel({
    cancelledAt: at(2),
    policy: { duringPreparation: "reject" },
    reason: "timeout",
  });

  const restored = RestaurantOrder.restore(
    structuredClone(order.toSnapshot()),
    3,
  );

  assert.deepEqual(restored.toSnapshot(), order.toSnapshot());
  assert.equal(restored.version, 3);
  assert.equal(restored.cancelledAtStage, "QUEUED");
  assert.deepEqual(restored.pullEvents(), []);
});

const openConditions = (
  menuItems: AcceptanceConditions["menuItems"],
): AcceptanceConditions => ({
  restaurant: {
    opensAt: at(0),
    closesAt: at(59),
    currentOrders: 0,
    maximumOrders: 10,
    scheduleException: false,
  },
  menuItems,
});

const offer = (remainingCapacity: number) => ({
  availableFrom: at(0),
  availableUntil: at(59),
  remainingCapacity,
});

test("acceptance is satisfied for an open restaurant with available items", () => {
  const result = evaluateOrderAcceptance({
    now: at(10),
    restaurant: { isOpen: true },
    menu: { findItem: () => ({ isAvailable: true }) },
    items: [{ menuItemId: "pho", quantity: 2 }],
    conditions: openConditions({ pho: offer(2) }),
  });

  assert.deepEqual(result, { satisfied: true, reasons: [] });
});

test("acceptance collects restaurant and item reasons", () => {
  const menuItems: Record<string, { isAvailable: boolean }> = {
    pho: { isAvailable: false },
    tea: { isAvailable: true },
    rice: { isAvailable: true },
  };
  const result = evaluateOrderAcceptance({
    now: at(10),
    restaurant: { isOpen: false },
    menu: { findItem: (id) => menuItems[id] },
    items: [
      { menuItemId: "pho", quantity: 1 },
      { menuItemId: "tea", quantity: 1 },
      { menuItemId: "tea", quantity: 1 },
      { menuItemId: "rice", quantity: 1 },
      { menuItemId: "unknown", quantity: 1 },
    ],
    conditions: {
      ...openConditions({ pho: offer(5), tea: offer(1) }),
      restaurant: {
        ...openConditions({}).restaurant,
        currentOrders: 10,
      },
    },
  });

  assert.equal(result.satisfied, false);
  assert.deepEqual(result.reasons, [
    { code: AcceptanceReasonCode.RestaurantClosed },
    { code: "capacity_exhausted" },
    { code: "temporarily_unavailable", menuItemId: "pho" },
    { code: "insufficient_capacity", menuItemId: "tea" },
    {
      code: AcceptanceReasonCode.MenuItemConditionsMissing,
      menuItemId: "rice",
    },
    { code: AcceptanceReasonCode.MenuItemNotFound, menuItemId: "unknown" },
  ]);
});

test("acceptance without a menu reports every item as not found", () => {
  const result = evaluateOrderAcceptance({
    now: at(10),
    restaurant: { isOpen: true },
    menu: undefined,
    items: [{ menuItemId: "pho", quantity: 1 }],
    conditions: openConditions({}),
  });

  assert.deepEqual(result.reasons, [
    { code: AcceptanceReasonCode.MenuItemNotFound, menuItemId: "pho" },
  ]);
});
