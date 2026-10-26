import type {
  OrderEndReason,
  OrderMilestoneKind,
  OrderStatus,
} from "../generated/graphql";
import {
  kitchenEight,
  type MockOrder,
  orderLine,
  orderTotal,
} from "./restaurant-fixtures";

// Fixed snapshots of the order lifecycle served by the mock BFF.

const minute = 60_000;

function at(now: Date, minutesFromNow: number): string {
  return new Date(now.getTime() + minutesFromNow * minute).toISOString();
}

const menu = kitchenEight.menu?.items ?? [];
const defaultItems = [orderLine(menu[0], 1), orderLine(menu[1], 2)];

type ScenarioSpec = {
  id: string;
  status: OrderStatus;
  endReason?: OrderEndReason;
  placedMinutesAgo: number;
  // Minutes after placing for each milestone after PLACED.
  milestones?: ReadonlyArray<readonly [OrderMilestoneKind, number]>;
  cancellableForMinutes?: number;
  // Minutes after placing for each deadline the kitchen or courier named.
  deadlines?: Readonly<{ ready?: number; pickup?: number; delivered?: number }>;
};

function buildOrder(spec: ScenarioSpec, now: Date): MockOrder {
  const placed = -spec.placedMinutesAgo;
  return {
    id: spec.id,
    status: spec.status,
    endReason: spec.endReason ?? null,
    restaurant: kitchenEight,
    items: defaultItems,
    total: orderTotal(defaultItems),
    placedAt: at(now, placed),
    milestones: [
      { kind: "PLACED", occurredAt: at(now, placed) },
      ...(spec.milestones ?? []).map(([kind, offset]) => ({
        kind,
        occurredAt: at(now, placed + offset),
      })),
    ],
    cancellation:
      spec.cancellableForMinutes === undefined
        ? null
        : { availableUntil: at(now, spec.cancellableForMinutes) },
    expectedReadyAt: deadline(now, placed, spec.deadlines?.ready),
    expectedPickupAt: deadline(now, placed, spec.deadlines?.pickup),
    expectedDeliveredAt: deadline(now, placed, spec.deadlines?.delivered),
  };
}

function deadline(
  now: Date,
  placed: number,
  offset: number | undefined,
): string | null {
  return offset === undefined ? null : at(now, placed + offset);
}

// The kitchen names readiness at acceptance, the courier pickup at
// assignment and delivery at pickup (FR-609, FR-610).
const preparingDeadlines = { ready: 22, pickup: 24 } as const;
const pickedUpDeadlines = { ...preparingDeadlines, delivered: 35 } as const;

const preparing = [
  ["CONFIRMED", 1],
  ["PREPARATION_STARTED", 4],
  ["COURIER_ASSIGNED", 9],
] as const;

const pickedUp = [
  ...preparing,
  ["READY_FOR_PICKUP", 18],
  ["PICKED_UP", 21],
] as const;

// Cooking without a courier yet: the header heads to readiness.
const cooking = [
  ["CONFIRMED", 1],
  ["PREPARATION_STARTED", 3],
] as const;

export const delayedOrderId = "order-delayed";
// order-delayed: the kitchen moves readiness this much later once someone
// follows the order, like PreparationDelayed (FR-611).
export const preparationDelayMinutes = 6;

export function preparationDelayed(order: MockOrder): MockOrder {
  if (order.expectedReadyAt === null) return order;
  return {
    ...order,
    expectedReadyAt: new Date(
      Date.parse(order.expectedReadyAt) + preparationDelayMinutes * minute,
    ).toISOString(),
  };
}

const scenarios: readonly ScenarioSpec[] = [
  {
    id: "order-received",
    status: "RECEIVED",
    placedMinutesAgo: 1,
    cancellableForMinutes: 4,
  },
  {
    id: "order-preparing",
    status: "PREPARING",
    placedMinutesAgo: 12,
    milestones: preparing,
    deadlines: preparingDeadlines,
  },
  {
    // Readiness in 10 minutes; the kitchen delays it while followed.
    id: delayedOrderId,
    status: "PREPARING",
    placedMinutesAgo: 8,
    milestones: cooking,
    deadlines: { ready: 18 },
  },
  {
    // Readiness was due two and a half minutes ago.
    id: "order-overdue",
    status: "PREPARING",
    placedMinutesAgo: 30,
    milestones: cooking,
    deadlines: { ready: 27.5 },
  },
  {
    // Delivery in 3 minutes: the screen counts down m:ss.
    id: "order-short",
    status: "PICKED_UP",
    placedMinutesAgo: 30,
    milestones: [
      ...preparing,
      ["READY_FOR_PICKUP", 18],
      ["PICKED_UP", 20],
    ],
    deadlines: { ready: 18, pickup: 20, delivered: 33 },
  },
  {
    id: "order-picked-up",
    status: "PICKED_UP",
    placedMinutesAgo: 25,
    milestones: pickedUp,
    deadlines: pickedUpDeadlines,
  },
  {
    id: "order-delivered",
    status: "DELIVERED",
    placedMinutesAgo: 90,
    milestones: [...pickedUp, ["DELIVERED", 34]],
    deadlines: pickedUpDeadlines,
  },
  {
    id: "order-cancelled",
    status: "CANCELLED",
    endReason: "CUSTOMER_CANCELLED",
    placedMinutesAgo: 180,
    milestones: [["ENDED", 2]],
  },
  {
    id: "order-restaurant-rejected",
    status: "FAILED",
    endReason: "RESTAURANT_REJECTED",
    placedMinutesAgo: 240,
    milestones: [["ENDED", 3]],
  },
  {
    id: "order-delivery-failed",
    status: "FAILED",
    endReason: "DELIVERY_FAILED",
    placedMinutesAgo: 300,
    milestones: [...pickedUp, ["ENDED", 52]],
    deadlines: pickedUpDeadlines,
  },
];

export function createOrderScenarios(now: Date): MockOrder[] {
  return scenarios.map((spec) => buildOrder(spec, now));
}

export const liveOrderId = "order-live";

// order-live walks the happy path one step per request.
const liveSteps: readonly ScenarioSpec[] = [
  {
    id: liveOrderId,
    status: "RECEIVED",
    placedMinutesAgo: 0,
    cancellableForMinutes: 5,
  },
  {
    id: liveOrderId,
    status: "CONFIRMED",
    placedMinutesAgo: 0,
    milestones: preparing.slice(0, 1),
    deadlines: { ready: preparingDeadlines.ready },
  },
  {
    id: liveOrderId,
    status: "PREPARING",
    placedMinutesAgo: 0,
    milestones: preparing,
    deadlines: preparingDeadlines,
  },
  {
    id: liveOrderId,
    status: "READY_FOR_PICKUP",
    placedMinutesAgo: 0,
    milestones: pickedUp.slice(0, 4),
    deadlines: preparingDeadlines,
  },
  {
    id: liveOrderId,
    status: "PICKED_UP",
    placedMinutesAgo: 0,
    milestones: pickedUp,
    deadlines: pickedUpDeadlines,
  },
  {
    id: liveOrderId,
    status: "DELIVERED",
    placedMinutesAgo: 0,
    milestones: [...pickedUp, ["DELIVERED", 34]],
    deadlines: pickedUpDeadlines,
  },
];

export const liveOrderStepCount = liveSteps.length;

export function liveOrderAt(step: number, startedAt: Date): MockOrder {
  return buildOrder(liveSteps[Math.min(step, liveSteps.length - 1)], startedAt);
}
