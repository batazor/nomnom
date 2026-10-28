import type {
  OrderEndReason,
  OrderMilestoneKind,
  OrderStatus,
} from "../../shared/graphql/generated/graphql";
import type { MessageKey } from "../../shared/i18n/messages";

export type StatusIconName =
  | "receipt"
  | "card"
  | "check"
  | "pot"
  | "bag"
  | "bike"
  | "home"
  | "cross"
  | "alert";

// Customer-facing progress steps; CANCELLED and FAILED replace the progress.
export const progressSteps = [
  "RECEIVED",
  "PAID",
  "CONFIRMED",
  "PREPARING",
  "READY_FOR_PICKUP",
  "PICKED_UP",
  "DELIVERED",
] as const satisfies readonly OrderStatus[];

type StatusPresentation = Readonly<{
  label: MessageKey;
  detail: MessageKey;
  icon: StatusIconName;
  // Index in progressSteps, null for statuses that end without delivery.
  step: number | null;
  final: boolean;
}>;

function step(status: (typeof progressSteps)[number]): number {
  return progressSteps.indexOf(status);
}

export const statusPresentation: Record<OrderStatus, StatusPresentation> = {
  RECEIVED: {
    label: "order.status.RECEIVED.label",
    detail: "order.status.RECEIVED.detail",
    icon: "receipt",
    step: step("RECEIVED"),
    final: false,
  },
  PAID: {
    label: "order.status.PAID.label",
    detail: "order.status.PAID.detail",
    icon: "card",
    step: step("PAID"),
    final: false,
  },
  // A scheduled order the restaurant accepted waits for the start its
  // window gives it: the confirmed step, with no cooking yet (FR-687).
  SCHEDULED: {
    label: "order.status.SCHEDULED.label",
    detail: "order.status.SCHEDULED.detail",
    icon: "check",
    step: step("CONFIRMED"),
    final: false,
  },
  CONFIRMED: {
    label: "order.status.CONFIRMED.label",
    detail: "order.status.CONFIRMED.detail",
    icon: "check",
    step: step("CONFIRMED"),
    final: false,
  },
  PREPARING: {
    label: "order.status.PREPARING.label",
    detail: "order.status.PREPARING.detail",
    icon: "pot",
    step: step("PREPARING"),
    final: false,
  },
  READY_FOR_PICKUP: {
    label: "order.status.READY_FOR_PICKUP.label",
    detail: "order.status.READY_FOR_PICKUP.detail",
    icon: "bag",
    step: step("READY_FOR_PICKUP"),
    final: false,
  },
  PICKED_UP: {
    label: "order.status.PICKED_UP.label",
    detail: "order.status.PICKED_UP.detail",
    icon: "bike",
    step: step("PICKED_UP"),
    final: false,
  },
  DELIVERED: {
    label: "order.status.DELIVERED.label",
    detail: "order.status.DELIVERED.detail",
    icon: "home",
    step: step("DELIVERED"),
    final: true,
  },
  CANCELLED: {
    label: "order.status.CANCELLED.label",
    detail: "order.status.CANCELLED.detail",
    icon: "cross",
    step: null,
    final: true,
  },
  FAILED: {
    label: "order.status.FAILED.label",
    detail: "order.status.FAILED.detail",
    icon: "alert",
    step: null,
    final: true,
  },
};

export const endReasonPresentation: Record<
  OrderEndReason,
  Readonly<{ title: MessageKey; detail: MessageKey }>
> = {
  CUSTOMER_CANCELLED: {
    title: "order.end.CUSTOMER_CANCELLED.title",
    detail: "order.end.CUSTOMER_CANCELLED.detail",
  },
  RESTAURANT_REJECTED: {
    title: "order.end.RESTAURANT_REJECTED.title",
    detail: "order.end.RESTAURANT_REJECTED.detail",
  },
  DELIVERY_FAILED: {
    title: "order.end.DELIVERY_FAILED.title",
    detail: "order.end.DELIVERY_FAILED.detail",
  },
  PAYMENT_DECLINED: {
    title: "order.end.PAYMENT_DECLINED.title",
    detail: "order.end.PAYMENT_DECLINED.detail",
  },
  NOT_FULFILLED: {
    title: "order.end.NOT_FULFILLED.title",
    detail: "order.end.NOT_FULFILLED.detail",
  },
};

export const milestoneLabel: Record<OrderMilestoneKind, MessageKey> = {
  PLACED: "order.milestone.PLACED",
  PAID: "order.milestone.PAID",
  CONFIRMED: "order.milestone.CONFIRMED",
  PREPARATION_STARTED: "order.milestone.PREPARATION_STARTED",
  READY_FOR_PICKUP: "order.milestone.READY_FOR_PICKUP",
  COURIER_ASSIGNED: "order.milestone.COURIER_ASSIGNED",
  PICKED_UP: "order.milestone.PICKED_UP",
  DELIVERED: "order.milestone.DELIVERED",
  ENDED: "order.milestone.ENDED",
};

// A FAILED or CANCELLED order without a reason still gets a neutral message.
export function endPresentation(endReason: OrderEndReason | null) {
  return endReasonPresentation[endReason ?? "NOT_FULFILLED"];
}

// The courier note belongs to the kitchen steps; once picked up it is implied.
export function showsCourierAssigned(
  status: OrderStatus,
  milestones: readonly { kind: OrderMilestoneKind }[],
): boolean {
  return (
    (status === "PREPARING" ||
      status === "READY_FOR_PICKUP" ||
      status === "CONFIRMED") &&
    milestones.some((milestone) => milestone.kind === "COURIER_ASSIGNED")
  );
}
