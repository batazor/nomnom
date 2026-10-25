import { RestaurantOrderState } from "./fsm.ts";

export const CancellationRejectionReason = {
  AlreadyReady: "already_ready",
  AlreadyRejected: "already_rejected",
  AlreadyFailed: "already_failed",
  CancellationDuringPreparationNotAllowed:
    "cancellation_during_preparation_not_allowed",
} as const;

export type CancellationRejectionReason =
  (typeof CancellationRejectionReason)[keyof typeof CancellationRejectionReason];

/**
 * Outcome of Cancel decided by the Restaurant Order by stage. Only
 * `cancelled` changes the State and records `RestaurantOrderCancelled`; a
 * rejection is a business answer, not a technical error.
 */
export type CancellationDecision =
  | Readonly<{
      outcome: "cancelled";
      /** State at the moment of cancellation. */
      stage: RestaurantOrderState;
    }>
  | Readonly<{
      /** The order had already been cancelled before this call. */
      outcome: "repeated";
      /** State in which the order was cancelled the first time. */
      stage: RestaurantOrderState;
    }>
  | Readonly<{
      outcome: "rejected";
      reason: CancellationRejectionReason;
      /** Current State, unchanged. */
      state: RestaurantOrderState;
    }>;

/** Terminal States after which Cancel is answered with a rejection. */
export const TERMINAL_CANCELLATION_REJECTIONS: Partial<
  Record<RestaurantOrderState, CancellationRejectionReason>
> = {
  [RestaurantOrderState.Ready]: CancellationRejectionReason.AlreadyReady,
  [RestaurantOrderState.Rejected]: CancellationRejectionReason.AlreadyRejected,
  [RestaurantOrderState.Failed]: CancellationRejectionReason.AlreadyFailed,
};
