/**
 * Pure FSM of the `RestaurantOrder` aggregate: `state + trigger + inputs →
 * next state | typed error`. The transition table is data and is the single
 * source of truth for allowed transitions; see docs/fsm.md#kitchen--preparation.
 * Item-level transitions (`ChangeItem`) are not part of the current model.
 */

export const RestaurantOrderState = {
  Received: "RECEIVED",
  Accepted: "ACCEPTED",
  Queued: "QUEUED",
  Preparing: "PREPARING",
  Assembled: "ASSEMBLED",
  Ready: "READY",
  Rejected: "REJECTED",
  Cancelled: "CANCELLED",
  Failed: "FAILED",
} as const;

export type RestaurantOrderState =
  (typeof RestaurantOrderState)[keyof typeof RestaurantOrderState];

export const RestaurantOrderTrigger = {
  Accept: "Accept",
  Reject: "Reject",
  Queue: "Queue",
  StartPreparation: "StartPreparation",
  MarkAssembled: "MarkAssembled",
  MarkReady: "MarkReady",
  ReportDelay: "ReportDelay",
  ReportFailure: "ReportFailure",
  Cancel: "Cancel",
} as const;

export type RestaurantOrderTrigger =
  (typeof RestaurantOrderTrigger)[keyof typeof RestaurantOrderTrigger];

export const ALL_STATES: readonly RestaurantOrderState[] =
  Object.values(RestaurantOrderState);
export const ALL_TRIGGERS: readonly RestaurantOrderTrigger[] = Object.values(
  RestaurantOrderTrigger,
);

export const TERMINAL_STATES: ReadonlySet<RestaurantOrderState> = new Set([
  RestaurantOrderState.Ready,
  RestaurantOrderState.Rejected,
  RestaurantOrderState.Cancelled,
  RestaurantOrderState.Failed,
]);

/** Typed, stable reason of a failed guard; `menuItemId` is set for item reasons. */
export type GuardFailureReason = Readonly<{
  code: string;
  menuItemId?: string;
}>;

export const GuardFailureCode = {
  NotLater: "not_later",
  CancellationDuringPreparationNotAllowed:
    "cancellation_during_preparation_not_allowed",
} as const;

/** Result of the Acceptance check computed before the `Accept` trigger. */
export type AcceptanceEligibility = Readonly<{
  satisfied: boolean;
  reasons: readonly GuardFailureReason[];
}>;

/**
 * Cancellation policy for orders whose Preparation already started. It is an
 * open product decision, so the composition root must choose it explicitly.
 */
export type CancellationPolicy = Readonly<{
  duringPreparation: "allow" | "reject";
}>;

export type TransitionRequest =
  | Readonly<{ trigger: "Accept"; eligibility: AcceptanceEligibility }>
  | Readonly<{ trigger: "Reject" }>
  | Readonly<{ trigger: "Queue" }>
  | Readonly<{ trigger: "StartPreparation" }>
  | Readonly<{ trigger: "MarkAssembled" }>
  | Readonly<{ trigger: "MarkReady" }>
  | Readonly<{
      trigger: "ReportDelay";
      now: Date;
      currentExpectedReadyAt: Date | undefined;
      requestedExpectedReadyAt: Date;
    }>
  | Readonly<{ trigger: "ReportFailure" }>
  | Readonly<{ trigger: "Cancel"; policy: CancellationPolicy }>;

type Guard = (request: TransitionRequest) => readonly GuardFailureReason[];

export type TransitionRow = Readonly<{
  from: RestaurantOrderState;
  trigger: RestaurantOrderTrigger;
  to: RestaurantOrderState;
  guard?: Guard;
}>;

const acceptanceGuard: Guard = (request) =>
  request.trigger === "Accept" && !request.eligibility.satisfied
    ? request.eligibility.reasons
    : [];

const laterExpectedReadyAtGuard: Guard = (request) => {
  if (request.trigger !== "ReportDelay") {
    return [];
  }
  const requested = request.requestedExpectedReadyAt.getTime();
  const current = request.currentExpectedReadyAt?.getTime();
  const later =
    requested > request.now.getTime() &&
    (current === undefined || requested > current);
  return later ? [] : [{ code: GuardFailureCode.NotLater }];
};

const cancellableDuringPreparationGuard: Guard = (request) =>
  request.trigger === "Cancel" && request.policy.duringPreparation !== "allow"
    ? [{ code: GuardFailureCode.CancellationDuringPreparationNotAllowed }]
    : [];

const S = RestaurantOrderState;
const T = RestaurantOrderTrigger;

export const TRANSITIONS: readonly TransitionRow[] = [
  {
    from: S.Received,
    trigger: T.Accept,
    to: S.Accepted,
    guard: acceptanceGuard,
  },
  { from: S.Received, trigger: T.Reject, to: S.Rejected },
  { from: S.Received, trigger: T.Cancel, to: S.Cancelled },
  { from: S.Accepted, trigger: T.Queue, to: S.Queued },
  { from: S.Accepted, trigger: T.Cancel, to: S.Cancelled },
  { from: S.Queued, trigger: T.StartPreparation, to: S.Preparing },
  {
    from: S.Queued,
    trigger: T.ReportDelay,
    to: S.Queued,
    guard: laterExpectedReadyAtGuard,
  },
  { from: S.Queued, trigger: T.Cancel, to: S.Cancelled },
  { from: S.Preparing, trigger: T.MarkAssembled, to: S.Assembled },
  {
    from: S.Preparing,
    trigger: T.ReportDelay,
    to: S.Preparing,
    guard: laterExpectedReadyAtGuard,
  },
  { from: S.Preparing, trigger: T.ReportFailure, to: S.Failed },
  {
    from: S.Preparing,
    trigger: T.Cancel,
    to: S.Cancelled,
    guard: cancellableDuringPreparationGuard,
  },
  { from: S.Assembled, trigger: T.MarkReady, to: S.Ready },
  {
    from: S.Assembled,
    trigger: T.ReportDelay,
    to: S.Assembled,
    guard: laterExpectedReadyAtGuard,
  },
  { from: S.Assembled, trigger: T.ReportFailure, to: S.Failed },
  {
    from: S.Assembled,
    trigger: T.Cancel,
    to: S.Cancelled,
    guard: cancellableDuringPreparationGuard,
  },
];

/** Strict repeats of an already applied transition: explicit `AlreadyInState`. */
export const REPEATS: readonly Readonly<{
  state: RestaurantOrderState;
  trigger: RestaurantOrderTrigger;
}>[] = [
  { state: S.Accepted, trigger: T.Accept },
  { state: S.Queued, trigger: T.Queue },
  { state: S.Preparing, trigger: T.StartPreparation },
  { state: S.Assembled, trigger: T.MarkAssembled },
  { state: S.Ready, trigger: T.MarkReady },
  { state: S.Rejected, trigger: T.Reject },
  { state: S.Cancelled, trigger: T.Cancel },
  { state: S.Failed, trigger: T.ReportFailure },
];

export class InvalidTransitionError extends Error {
  readonly code = "invalid_transition";
  readonly from: RestaurantOrderState;
  readonly trigger: RestaurantOrderTrigger;

  constructor(from: RestaurantOrderState, trigger: RestaurantOrderTrigger) {
    super(`trigger ${trigger} is not allowed from ${from}`);
    this.name = "InvalidTransitionError";
    this.from = from;
    this.trigger = trigger;
  }
}

export class GuardFailedError extends Error {
  readonly code = "guard_failed";
  readonly trigger: RestaurantOrderTrigger;
  readonly reasons: readonly GuardFailureReason[];

  constructor(
    trigger: RestaurantOrderTrigger,
    reasons: readonly GuardFailureReason[],
  ) {
    super(
      `guard of ${trigger} failed: ${reasons.map((reason) => reason.code).join(", ")}`,
    );
    this.name = "GuardFailedError";
    this.trigger = trigger;
    this.reasons = reasons;
  }
}

export class AlreadyInStateError extends Error {
  readonly code = "already_in_state";
  readonly state: RestaurantOrderState;

  constructor(state: RestaurantOrderState) {
    super(`restaurant order is already ${state}`);
    this.name = "AlreadyInStateError";
    this.state = state;
  }
}

export type TransitionError =
  | InvalidTransitionError
  | GuardFailedError
  | AlreadyInStateError;

export type TransitionOutcome =
  | Readonly<{
      ok: true;
      from: RestaurantOrderState;
      trigger: RestaurantOrderTrigger;
      to: RestaurantOrderState;
    }>
  | Readonly<{ ok: false; error: TransitionError }>;

/** Resolves the outcome of `request.trigger` from `state` without side effects. */
export const resolveTransition = (
  state: RestaurantOrderState,
  request: TransitionRequest,
): TransitionOutcome => {
  const row = TRANSITIONS.find(
    (candidate) =>
      candidate.from === state && candidate.trigger === request.trigger,
  );
  if (row !== undefined) {
    const reasons = row.guard?.(request) ?? [];
    if (reasons.length > 0) {
      return {
        ok: false,
        error: new GuardFailedError(request.trigger, reasons),
      };
    }
    return { ok: true, from: state, trigger: request.trigger, to: row.to };
  }
  const repeat = REPEATS.some(
    (candidate) =>
      candidate.state === state && candidate.trigger === request.trigger,
  );
  return {
    ok: false,
    error: repeat
      ? new AlreadyInStateError(state)
      : new InvalidTransitionError(state, request.trigger),
  };
};
