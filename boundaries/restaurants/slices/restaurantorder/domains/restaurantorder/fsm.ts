/**
 * Pure statechart of the `RestaurantOrder` aggregate: `leaf state + trigger +
 * inputs → next leaf state | typed error`. States form a hierarchy; a
 * transition declared on a compound state applies to every state inside it,
 * and the innermost declaration wins. The table and the hierarchy are data
 * and the single source of truth for allowed transitions; see
 * docs/fsm.md#kitchen--preparation and ADR 41. Item-level transitions
 * (`ChangeItem`) are not part of the current model.
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

/**
 * Compound states: named groups of the Ubiquitous Language. They are never
 * persisted or published; the aggregate is always in exactly one leaf state.
 */
export const RestaurantOrderCompoundState = {
  /** Kitchen work is not finished; the order can still be cancelled. */
  Open: "OPEN",
  /** The order holds a place in the Kitchen Queue and an expected ready time. */
  InKitchen: "IN_KITCHEN",
  /** Kitchen resources are being spent on the order. */
  ActivePreparation: "ACTIVE_PREPARATION",
  /** Terminal states. */
  Closed: "CLOSED",
} as const;

export type RestaurantOrderCompoundState =
  (typeof RestaurantOrderCompoundState)[keyof typeof RestaurantOrderCompoundState];

export type RestaurantOrderNode =
  | RestaurantOrderState
  | RestaurantOrderCompoundState;

const S = RestaurantOrderState;
const C = RestaurantOrderCompoundState;
const T = RestaurantOrderTrigger;

/** Parent of every node; `null` marks a top-level node. */
export const PARENT: Readonly<
  Record<RestaurantOrderNode, RestaurantOrderCompoundState | null>
> = {
  [C.Open]: null,
  [C.Closed]: null,
  [S.Received]: C.Open,
  [S.Accepted]: C.Open,
  [C.InKitchen]: C.Open,
  [S.Queued]: C.InKitchen,
  [C.ActivePreparation]: C.InKitchen,
  [S.Preparing]: C.ActivePreparation,
  [S.Assembled]: C.ActivePreparation,
  [S.Ready]: C.Closed,
  [S.Rejected]: C.Closed,
  [S.Cancelled]: C.Closed,
  [S.Failed]: C.Closed,
};

/** `node` followed by its ancestors up to the top level. */
export const lineage = (
  node: RestaurantOrderNode,
): readonly RestaurantOrderNode[] => {
  const nodes: RestaurantOrderNode[] = [];
  for (
    let current: RestaurantOrderNode | null = node;
    current !== null;
    current = PARENT[current]
  ) {
    nodes.push(current);
  }
  return nodes;
};

/** Whether `state` is `node` or lies inside it. */
export const isIn = (
  state: RestaurantOrderState,
  node: RestaurantOrderNode,
): boolean => lineage(state).includes(node);

/** Leaf states inside `node`, in declaration order. */
export const leavesOf = (
  node: RestaurantOrderNode,
): readonly RestaurantOrderState[] =>
  ALL_STATES.filter((state) => isIn(state, node));

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

/** Targetless transition: the aggregate stays in its current leaf state. */
export const STAY = "stay";

export type TransitionRow = Readonly<{
  source: RestaurantOrderNode;
  trigger: RestaurantOrderTrigger;
  target: RestaurantOrderState | typeof STAY;
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

export const TRANSITIONS: readonly TransitionRow[] = [
  {
    source: S.Received,
    trigger: T.Accept,
    target: S.Accepted,
    guard: acceptanceGuard,
  },
  { source: S.Received, trigger: T.Reject, target: S.Rejected },
  { source: S.Accepted, trigger: T.Queue, target: S.Queued },
  { source: S.Queued, trigger: T.StartPreparation, target: S.Preparing },
  { source: S.Preparing, trigger: T.MarkAssembled, target: S.Assembled },
  { source: S.Assembled, trigger: T.MarkReady, target: S.Ready },
  { source: C.Open, trigger: T.Cancel, target: S.Cancelled },
  {
    source: C.InKitchen,
    trigger: T.ReportDelay,
    target: STAY,
    guard: laterExpectedReadyAtGuard,
  },
  { source: C.ActivePreparation, trigger: T.ReportFailure, target: S.Failed },
  {
    source: C.ActivePreparation,
    trigger: T.Cancel,
    target: S.Cancelled,
    guard: cancellableDuringPreparationGuard,
  },
];

/**
 * Strict repeats of an already applied transition: explicit `AlreadyInState`.
 * Declared on leaf states only.
 */
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

/**
 * Resolves the outcome of `request.trigger` from `state` without side effects:
 * the innermost declaration on `state` or one of its ancestors wins.
 */
export const resolveTransition = (
  state: RestaurantOrderState,
  request: TransitionRequest,
): TransitionOutcome => {
  for (const node of lineage(state)) {
    const row = TRANSITIONS.find(
      (candidate) =>
        candidate.source === node && candidate.trigger === request.trigger,
    );
    if (row !== undefined) {
      const reasons = row.guard?.(request) ?? [];
      if (reasons.length > 0) {
        return {
          ok: false,
          error: new GuardFailedError(request.trigger, reasons),
        };
      }
      const to = row.target === STAY ? state : row.target;
      return { ok: true, from: state, trigger: request.trigger, to };
    }
    const repeat = REPEATS.some(
      (candidate) =>
        candidate.state === node && candidate.trigger === request.trigger,
    );
    if (repeat) {
      return { ok: false, error: new AlreadyInStateError(state) };
    }
  }
  return {
    ok: false,
    error: new InvalidTransitionError(state, request.trigger),
  };
};
