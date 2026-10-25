import type { OrderMilestoneKind } from "../../shared/graphql/generated/graphql";
import type { MessageKey } from "../../shared/i18n/messages";
import type { ViewedOrder } from "./use-order-updates";

// Progress of cooking and delivery towards the deadlines the kitchen and the
// courier named (FR-612). A deadline is a fact of an event; between events
// the screen counts the progress itself, without requests (ADR 36).

export type EtaStage = "cooking" | "delivery";

export type EtaBar =
  // The stage runs towards a named deadline from the moment it started.
  | {
      stage: EtaStage;
      kind: "progress";
      label: MessageKey;
      startedAt: number;
      deadline: number;
    }
  // The stage started, but nobody named its deadline yet.
  | { stage: EtaStage; kind: "unnamed"; label: MessageKey }
  // The stage finished; finishedAt is the moment of its milestone.
  | {
      stage: EtaStage;
      kind: "done";
      label: MessageKey;
      finishedAt: string | null;
    };

type Milestones = ViewedOrder["milestones"];

// The latest moment of a milestone: a reassigned courier repeats it.
function lastMoment(
  milestones: Milestones,
  kind: OrderMilestoneKind,
): string | null {
  const found = milestones.filter((milestone) => milestone.kind === kind);
  return found.length === 0 ? null : found[found.length - 1].occurredAt;
}

function bar(
  stage: EtaStage,
  label: MessageKey,
  startedAt: string | null,
  deadline: string | null,
): EtaBar | null {
  // Without the start moment there is nothing to count from: no bar.
  if (startedAt === null) return null;
  if (deadline === null) return { stage, kind: "unnamed", label };
  return {
    stage,
    kind: "progress",
    label,
    startedAt: Date.parse(startedAt),
    deadline: Date.parse(deadline),
  };
}

const cookingFinished = new Set<ViewedOrder["status"]>([
  "READY_FOR_PICKUP",
  "PICKED_UP",
  "DELIVERED",
]);

// Cooking runs from the kitchen acceptance (CONFIRMED) to expectedReadyAt.
function cookingBar(order: ViewedOrder): EtaBar | null {
  const ready = lastMoment(order.milestones, "READY_FOR_PICKUP");
  if (ready !== null || cookingFinished.has(order.status)) {
    return {
      stage: "cooking",
      kind: "done",
      label: "order.eta.cooking",
      finishedAt: ready,
    };
  }
  return bar(
    "cooking",
    "order.eta.cooking",
    lastMoment(order.milestones, "CONFIRMED"),
    order.expectedReadyAt,
  );
}

// Delivery runs from the courier assignment to expectedDeliveredAt; before
// the pickup, to expectedPickupAt.
function deliveryBar(order: ViewedOrder): EtaBar | null {
  const delivered = lastMoment(order.milestones, "DELIVERED");
  if (delivered !== null || order.status === "DELIVERED") {
    return {
      stage: "delivery",
      kind: "done",
      label: "order.eta.delivery",
      finishedAt: delivered,
    };
  }
  const assigned = lastMoment(order.milestones, "COURIER_ASSIGNED");
  const pickedUp =
    order.status === "PICKED_UP" ||
    lastMoment(order.milestones, "PICKED_UP") !== null;
  return pickedUp
    ? bar("delivery", "order.eta.delivery", assigned, order.expectedDeliveredAt)
    : bar("delivery", "order.eta.toPickup", assigned, order.expectedPickupAt);
}

// Bars of an order that is still on its way; a final order shows none.
export function etaBars(order: ViewedOrder): EtaBar[] {
  if (
    order.status === "DELIVERED" ||
    order.status === "CANCELLED" ||
    order.status === "FAILED"
  ) {
    return [];
  }
  return [cookingBar(order), deliveryBar(order)].filter(
    (found): found is EtaBar => found !== null,
  );
}

// Whether any bar counts towards a deadline, so the screen needs a clock.
export function runsClock(bars: readonly EtaBar[]): boolean {
  return bars.some((found) => found.kind === "progress");
}

export type EtaRemaining =
  | { kind: "delayed" }
  | { kind: "lessThanMinute" }
  | { kind: "minutes"; minutes: number };

// Progress in whole percent, 0..100, and the time left at now.
export function etaProgress(
  found: Extract<EtaBar, { kind: "progress" }>,
  now: number,
): { percent: number; remaining: EtaRemaining } {
  const total = found.deadline - found.startedAt;
  const elapsed = now - found.startedAt;
  const percent =
    total <= 0
      ? 100
      : Math.min(100, Math.max(0, Math.round((elapsed / total) * 100)));
  const left = found.deadline - now;
  if (left <= 0) return { percent: 100, remaining: { kind: "delayed" } };
  if (left < 60_000) return { percent, remaining: { kind: "lessThanMinute" } };
  return {
    percent,
    remaining: { kind: "minutes", minutes: Math.ceil(left / 60_000) },
  };
}
