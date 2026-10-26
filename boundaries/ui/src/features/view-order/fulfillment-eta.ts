import type { EtaSpan } from "../../shared/eta";
import type { OrderMilestoneKind } from "../../shared/graphql/generated/graphql";
import type { MessageKey } from "../../shared/i18n/messages";
import type { ViewedOrder } from "./use-order-updates";

// Deadlines the kitchen and the courier named, shown on the order screen
// (FR-612). A deadline is a fact of an event; between events the screen
// counts the time itself, without requests (ADR 36; shared/eta). Pure
// functions over the order.

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

export type StepEta = EtaSpan & {
  stage: EtaStage;
  label: MessageKey;
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
