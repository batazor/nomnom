import type { OrderMilestoneKind } from "../../shared/graphql/generated/graphql";
import type { MessageKey } from "../../shared/i18n/messages";
import type { ViewedOrder } from "./use-order-updates";

// Deadlines the kitchen and the courier named, shown on the order screen
// (FR-612). A deadline is a fact of an event; between events the screen
// counts the time itself, without requests (ADR 36). Pure functions: the
// clock comes in as `now`.

const minuteMs = 60_000;
// Up to this much time left the screen counts down m:ss every second.
export const countdownMs = 5 * minuteMs;

type Milestones = ViewedOrder["milestones"];

const finalStatuses = new Set<ViewedOrder["status"]>([
  "DELIVERED",
  "CANCELLED",
  "FAILED",
]);

// The latest moment of a milestone: a reassigned courier repeats it.
function lastMoment(
  milestones: Milestones,
  kind: OrderMilestoneKind,
): string | null {
  const found = milestones.filter((milestone) => milestone.kind === kind);
  return found.length === 0 ? null : found[found.length - 1].occurredAt;
}

export type HeaderEtaKind = "delivered" | "pickup" | "ready";

export type HeaderEta = { kind: HeaderEtaKind; deadline: number };

// The best known deadline of an order on its way: delivery, else pickup,
// else readiness. Stages are not summed; no deadline, no line.
export function headerEta(order: ViewedOrder): HeaderEta | null {
  if (finalStatuses.has(order.status)) return null;
  if (order.expectedDeliveredAt !== null) {
    return {
      kind: "delivered",
      deadline: Date.parse(order.expectedDeliveredAt),
    };
  }
  if (order.expectedPickupAt !== null) {
    return { kind: "pickup", deadline: Date.parse(order.expectedPickupAt) };
  }
  if (order.expectedReadyAt !== null) {
    return { kind: "ready", deadline: Date.parse(order.expectedReadyAt) };
  }
  return null;
}

export type EtaStage = "cooking" | "toPickup" | "delivery";

export type StepEta = {
  stage: EtaStage;
  label: MessageKey;
  startedAt: number;
  deadline: number;
};

const stageLabel: Record<EtaStage, MessageKey> = {
  cooking: "order.eta.cooking",
  toPickup: "order.eta.toPickup",
  delivery: "order.eta.delivery",
};

function stepEta(
  stage: EtaStage,
  startedAt: string | null,
  deadline: string | null,
): StepEta | null {
  // Without a start or a deadline there is nothing to count: no bar.
  if (startedAt === null || deadline === null) return null;
  return {
    stage,
    label: stageLabel[stage],
    startedAt: Date.parse(startedAt),
    deadline: Date.parse(deadline),
  };
}

// The bar of the active step. Cooking (CONFIRMED, PREPARING) runs from the
// latest kitchen acceptance to expectedReadyAt. The courier step before the
// pickup (READY_FOR_PICKUP) runs from the latest assignment to
// expectedPickupAt; after it (PICKED_UP), from the pickup to
// expectedDeliveredAt, the leg the courier named that deadline for.
export function activeStepEta(order: ViewedOrder): StepEta | null {
  const { milestones } = order;
  switch (order.status) {
    case "CONFIRMED":
    case "PREPARING":
      return stepEta(
        "cooking",
        lastMoment(milestones, "CONFIRMED"),
        order.expectedReadyAt,
      );
    case "READY_FOR_PICKUP":
      return stepEta(
        "toPickup",
        lastMoment(milestones, "COURIER_ASSIGNED"),
        order.expectedPickupAt,
      );
    case "PICKED_UP":
      return stepEta(
        "delivery",
        lastMoment(milestones, "PICKED_UP"),
        order.expectedDeliveredAt,
      );
    case "RECEIVED":
    case "DELIVERED":
    case "CANCELLED":
    case "FAILED":
      return null;
  }
}

export type EtaRemaining =
  // More than five minutes left: whole minutes, rounded up.
  | { kind: "minutes"; minutes: number }
  // Five minutes or less: a m:ss countdown.
  | { kind: "countdown"; minutes: number; seconds: number }
  // The deadline passed this many minutes ago, rounded up.
  | { kind: "overdue"; minutes: number };

export function etaRemaining(deadline: number, now: number): EtaRemaining {
  const left = deadline - now;
  if (left <= 0) {
    return { kind: "overdue", minutes: Math.max(1, Math.ceil(-left / minuteMs)) };
  }
  if (left > countdownMs) {
    return { kind: "minutes", minutes: Math.ceil(left / minuteMs) };
  }
  const totalSeconds = Math.ceil(left / 1_000);
  return {
    kind: "countdown",
    minutes: Math.floor(totalSeconds / 60),
    seconds: totalSeconds % 60,
  };
}

// The m:ss text of a countdown.
export function countdownText(
  remaining: Extract<EtaRemaining, { kind: "countdown" }>,
): string {
  return `${remaining.minutes}:${String(remaining.seconds).padStart(2, "0")}`;
}

// Whole minutes left, rounded up, for text that must not change every second.
export function minutesLeft(deadline: number, now: number): number {
  return Math.max(1, Math.ceil((deadline - now) / minuteMs));
}

// Elapsed share of the stage, 0..100, not rounded.
export function etaPercent(eta: StepEta, now: number): number {
  const total = eta.deadline - eta.startedAt;
  if (total <= 0) return 100;
  return Math.min(100, Math.max(0, ((now - eta.startedAt) / total) * 100));
}

// aria-valuenow moves in steps of 5%, not with every tick.
export function ariaPercent(percent: number): number {
  return Math.min(100, Math.max(0, Math.round(percent / 5) * 5));
}

// Whole minutes a deadline of the same stage moved later; null if it did not.
export function deadlineShiftMinutes(
  previous: number,
  next: number,
): number | null {
  const minutes = Math.round((next - previous) / minuteMs);
  return minutes > 0 ? minutes : null;
}
