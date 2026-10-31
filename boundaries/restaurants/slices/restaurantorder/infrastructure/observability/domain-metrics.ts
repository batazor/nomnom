// Business metrics of the Kitchen Order slice (domain and kitchen sections of
// ops/grafana/inventory; names, units and attributes from
// ops/semconv/model/domain.yaml and kitchen.yaml). Counters change only at an
// idempotently confirmed transition: by the Domain Events written to the
// Outbox, after the outermost Unit of Work commits. The domain does not know
// about metrics: the composition root decorates the Unit of Work, the Outbox
// and Receive Order Request with the types below.
//
// FSM transitions (fsm section; ops/semconv/model/fsm.yaml) are observed the
// same way (ADR 21): an applied transition is derived from the Domain Events
// of a saved Restaurant Order and the transition table, and recorded once the
// save commits; a rejected one is an InvalidTransitionError of a kitchen
// command or a cancellation rejected in a terminal state.
import { AsyncLocalStorage } from "node:async_hooks";

import { type Counter, type Meter, metrics } from "@opentelemetry/api";
import type { MongoClient } from "mongodb";
import { DatabaseUnitOfWork } from "../../../../infrastructure/repository/unit-of-work.ts";
import type {
  AggregateRef,
  Outbox,
  PublishableEvent,
} from "../../../../pkg/outbox/outbox.ts";
import type { UnitOfWork } from "../../../../pkg/unit-of-work/unit-of-work.ts";
import type {
  ReceiveOrderRequest,
  ReceiveOrderRequestHandler,
  ReceiveOrderRequestResult,
} from "../../applications/acceptance/receive-request/receive-request.ts";
import type { CancelRestaurantOrderResult } from "../../applications/cancel/cancel.ts";
import {
  ALL_STATES,
  InvalidTransitionError,
  isIn,
  RestaurantOrderCompoundState,
  RestaurantOrderTrigger,
  STAY,
  TRANSITIONS,
} from "../../domains/restaurantorder/fsm.ts";
import type {
  RestaurantOrder,
  RestaurantOrderEvent,
  RestaurantOrderEventType,
  RestaurantOrderRepository,
  RestaurantOrderState,
} from "../../domains/restaurantorder/restaurantorder.ts";
import { RESTAURANT_ORDER_AGGREGATE_TYPE } from "../integration/restaurant-order-events.ts";

export const DOMAIN_INSTRUMENTATION =
  "nomnom/restaurants/slices/restaurantorder/domain";

const REASON = "nomnom.reason";
/** Label of a reason outside the closed set, or of a missing reason. */
export const OTHER_REASON = "other";

/**
 * Closed sets of the `nomnom.reason` label. Reject Order and Report
 * Preparation Delay take an opaque reason; the approved closed sets below (ops/semconv) are counted, so
 * any other value is recorded as {@link OTHER_REASON}: a typo or free text
 * never creates a new series.
 */
export type ReasonCodes = Readonly<{
  rejection: readonly string[];
  delay: readonly string[];
}>;

export const DEFAULT_REASON_CODES: ReasonCodes = {
  rejection: ["too_busy", "item_unavailable", "restaurant_closed"],
  delay: ["too_busy", "item_unavailable"],
};

// Attributes of nomnom.fsm.transitions from the registry ops/semconv.
const FSM_AGGREGATE_TYPE = "nomnom.fsm.aggregate_type";
const FSM_FROM_STATE = "nomnom.fsm.from_state";
const FSM_TO_STATE = "nomnom.fsm.to_state";
const FSM_TRIGGER = "nomnom.fsm.trigger";
const FSM_RESULT = "nomnom.fsm.result";
const FSM_TERMINAL = "nomnom.fsm.terminal";

/** `nomnom.fsm.aggregate_type` of the Restaurant Order FSM. */
export const FSM_RESTAURANT_ORDER = "restaurant_order";

/**
 * Trigger whose transition records each event; `RestaurantOrderReceived`
 * creates the order and is not a transition.
 */
const TRIGGER_BY_EVENT: Readonly<
  Record<RestaurantOrderEventType, RestaurantOrderTrigger | undefined>
> = {
  RestaurantOrderReceived: undefined,
  RestaurantOrderAccepted: RestaurantOrderTrigger.Accept,
  RestaurantOrderRejected: RestaurantOrderTrigger.Reject,
  RestaurantOrderQueued: RestaurantOrderTrigger.Queue,
  PreparationStarted: RestaurantOrderTrigger.StartPreparation,
  RestaurantOrderAssembled: RestaurantOrderTrigger.MarkAssembled,
  RestaurantOrderReady: RestaurantOrderTrigger.MarkReady,
  RestaurantOrderPickedUp: RestaurantOrderTrigger.RecordPickup,
  PreparationDelayed: RestaurantOrderTrigger.ReportDelay,
  RestaurantOrderFailed: RestaurantOrderTrigger.ReportFailure,
  RestaurantOrderCancelled: RestaurantOrderTrigger.Cancel,
};

export type FsmTransition = Readonly<{
  from: RestaurantOrderState;
  trigger: RestaurantOrderTrigger;
  to: RestaurantOrderState;
  result: "applied" | "rejected";
}>;

/** Terminal states: leaves of the compound state Closed. */
const isTerminal = (state: RestaurantOrderState): boolean =>
  isIn(state, RestaurantOrderCompoundState.Closed);

/**
 * Applied transitions behind `events`, oldest first, of an order now in
 * `state`. Walks back from `state`: the row of each event's trigger ends in
 * the state after it (a targetless row keeps it) and its source contains the
 * state before it. A row on a compound state names no single source, so the
 * event's `stage` gives it. An event outside the statechart is a defect and
 * throws.
 */
export const appliedTransitions = (
  state: RestaurantOrderState,
  events: readonly RestaurantOrderEvent[],
): FsmTransition[] => {
  const transitions: FsmTransition[] = [];
  let to = state;
  for (const event of [...events].reverse()) {
    const trigger = TRIGGER_BY_EVENT[event.type];
    if (trigger === undefined) continue;
    const stage = "stage" in event ? event.stage : undefined;
    const row = TRANSITIONS.find(
      (candidate) =>
        candidate.trigger === trigger &&
        (candidate.target === STAY
          ? isIn(to, candidate.source)
          : candidate.target === to) &&
        (stage === undefined || isIn(stage, candidate.source)),
    );
    if (row === undefined) {
      throw new Error(`${event.type} has no FSM transition into ${to}`);
    }
    const from =
      row.target === STAY
        ? to
        : (stage ?? ALL_STATES.find((leaf) => leaf === row.source));
    if (from === undefined) {
      throw new Error(`${event.type} into ${to} has no stage of ${row.source}`);
    }
    transitions.unshift({ from, trigger, to, result: "applied" });
    to = from;
  }
  return transitions;
};

type Scope = { events: PublishableEvent[]; transitions: FsmTransition[] };

/**
 * Domain metrics of Restaurant Orders. Events reported by {@link appended}
 * inside {@link track} are recorded once the outermost scope succeeds; a
 * failed scope drops them. Outside a scope the write is already committed, so
 * events are recorded at once. Instruments live on the global MeterProvider
 * by default and are no-op until telemetry starts.
 */
export class RestaurantOrderDomainMetrics {
  readonly #scope = new AsyncLocalStorage<Scope>();
  readonly #received: Counter;
  readonly #accepted: Counter;
  readonly #rejected: Counter;
  readonly #delays: Counter;
  readonly #transitions: Counter;
  readonly #rejectionReasons: ReadonlySet<string>;
  readonly #delayReasons: ReadonlySet<string>;

  constructor(
    options: {
      meter?: Meter;
      reasons?: ReasonCodes;
    } = {},
  ) {
    const meter = options.meter ?? metrics.getMeter(DOMAIN_INSTRUMENTATION);
    const reasons = options.reasons ?? DEFAULT_REASON_CODES;
    this.#rejectionReasons = new Set(reasons.rejection);
    this.#delayReasons = new Set(reasons.delay);
    const orders = (name: string, description: string) =>
      meter.createCounter(name, { unit: "{order}", description });
    this.#received = orders(
      "nomnom.restaurants.orders.received",
      "Order requests received by restaurants (Restaurant Order created).",
    );
    this.#accepted = orders(
      "nomnom.restaurants.orders.accepted",
      "Order requests accepted by restaurants (RestaurantOrderAccepted committed).",
    );
    this.#rejected = orders(
      "nomnom.restaurants.orders.rejected",
      "Order requests rejected by restaurants, by reason (RestaurantOrderRejected committed).",
    );
    this.#delays = meter.createCounter("nomnom.kitchen.delays", {
      unit: "{delay}",
      description:
        "Preparation delays by reason (PreparationDelayed committed).",
    });
    this.#transitions = meter.createCounter("nomnom.fsm.transitions", {
      unit: "{transition}",
      description: "Applied FSM transitions.",
    });
  }

  /**
   * Runs one Unit of Work through `run`. The outermost call opens the scope
   * and clears it at the start of every attempt, so a retried transaction is
   * counted once; a nested call joins the outer scope.
   */
  async track<T>(
    run: (work: () => Promise<T>) => Promise<T>,
    work: () => Promise<T>,
  ): Promise<T> {
    if (this.#scope.getStore() !== undefined) {
      return run(work);
    }
    const scope: Scope = { events: [], transitions: [] };
    const result = await this.#scope.run(scope, () =>
      run(() => {
        scope.events = [];
        scope.transitions = [];
        return work();
      }),
    );
    this.#record(scope.events);
    this.#recordTransitions(scope.transitions);
    return result;
  }

  /** Restaurant Order events successfully written with their state change. */
  appended(events: readonly PublishableEvent[]): void {
    const scope = this.#scope.getStore();
    if (scope !== undefined) {
      scope.events.push(...events);
      return;
    }
    this.#record(events);
  }

  /** Applied transitions of a successfully saved Restaurant Order. */
  transitioned(transitions: readonly FsmTransition[]): void {
    const scope = this.#scope.getStore();
    if (scope !== undefined) {
      scope.transitions.push(...transitions);
      return;
    }
    this.#recordTransitions(transitions);
  }

  /** A trigger the table does not allow: nothing changed, nothing to commit. */
  rejected(from: RestaurantOrderState, trigger: RestaurantOrderTrigger): void {
    this.#recordTransitions([{ from, trigger, to: from, result: "rejected" }]);
  }

  #recordTransitions(transitions: readonly FsmTransition[]): void {
    for (const transition of transitions) {
      this.#transitions.add(1, {
        [FSM_AGGREGATE_TYPE]: FSM_RESTAURANT_ORDER,
        [FSM_FROM_STATE]: transition.from.toLowerCase(),
        [FSM_TO_STATE]: transition.to.toLowerCase(),
        [FSM_TRIGGER]: snakeCase(transition.trigger),
        [FSM_RESULT]: transition.result,
        [FSM_TERMINAL]: isTerminal(transition.to),
      });
    }
  }

  #record(events: readonly PublishableEvent[]): void {
    for (const event of events) {
      switch (event.type as RestaurantOrderEventType) {
        case "RestaurantOrderReceived":
          this.#received.add(1);
          break;
        case "RestaurantOrderAccepted":
          this.#accepted.add(1);
          break;
        case "RestaurantOrderRejected":
          this.#rejected.add(1, {
            [REASON]: label(this.#rejectionReasons, event),
          });
          break;
        case "PreparationDelayed":
          this.#delays.add(1, { [REASON]: label(this.#delayReasons, event) });
          break;
      }
    }
  }
}

/** `StartPreparation` → `start_preparation`. */
const snakeCase = (trigger: RestaurantOrderTrigger): string =>
  trigger.replace(/(?<!^)([A-Z])/g, "_$1").toLowerCase();

const label = (codes: ReadonlySet<string>, event: PublishableEvent): string => {
  const reason = (event as { reason?: unknown }).reason;
  return typeof reason === "string" && codes.has(reason)
    ? reason
    : OTHER_REASON;
};

/** Unit of Work port decorated with {@link RestaurantOrderDomainMetrics}. */
export class ObservedUnitOfWork implements UnitOfWork {
  readonly #inner: UnitOfWork;
  readonly #metrics: RestaurantOrderDomainMetrics;

  constructor(inner: UnitOfWork, metrics: RestaurantOrderDomainMetrics) {
    this.#inner = inner;
    this.#metrics = metrics;
  }

  run<T>(work: () => Promise<T>): Promise<T> {
    return this.#metrics.track((attempt) => this.#inner.run(attempt), work);
  }
}

/**
 * {@link DatabaseUnitOfWork} of the processes: repositories, the Outbox and the
 * Inbox need its session, so the decorator keeps the concrete type.
 */
export class ObservedDatabaseUnitOfWork extends DatabaseUnitOfWork {
  readonly #metrics: RestaurantOrderDomainMetrics;

  constructor(client: MongoClient, metrics: RestaurantOrderDomainMetrics) {
    super(client);
    this.#metrics = metrics;
  }

  override run<T>(work: () => Promise<T>): Promise<T> {
    return this.#metrics.track((attempt) => super.run(attempt), work);
  }
}

/** Outbox port that reports written Restaurant Order events. */
export class ObservedOutbox implements Outbox {
  readonly #inner: Outbox;
  readonly #metrics: RestaurantOrderDomainMetrics;

  constructor(inner: Outbox, metrics: RestaurantOrderDomainMetrics) {
    this.#inner = inner;
    this.#metrics = metrics;
  }

  async add(
    aggregate: AggregateRef,
    events: readonly PublishableEvent[],
  ): Promise<void> {
    await this.#inner.add(aggregate, events);
    if (aggregate.type === RESTAURANT_ORDER_AGGREGATE_TYPE) {
      this.#metrics.appended(events);
    }
  }
}

export type ReceiveOrderRequestPort = Pick<
  ReceiveOrderRequestHandler,
  "execute"
>;

/**
 * `RestaurantOrderReceived` is a local fact without the Outbox: the
 * confirmed transition is `created: true` (a repeat, a concurrent duplicate
 * and an Inbox duplicate are not). Inside the consumer's Inbox transaction it
 * is recorded after that transaction commits.
 */
export const observeReceiveOrderRequest = (
  handler: ReceiveOrderRequestPort,
  metrics: RestaurantOrderDomainMetrics,
): ReceiveOrderRequestPort => ({
  async execute(
    command: ReceiveOrderRequest,
  ): Promise<ReceiveOrderRequestResult> {
    const result = await handler.execute(command);
    if (result.created) {
      metrics.appended([
        { type: "RestaurantOrderReceived", occurredAt: new Date() },
      ]);
    }
    return result;
  },
});

/**
 * Restaurant Order repository that reports the applied transitions of every
 * successful save. They are derived before the write, so a defect of the
 * mapping fails the save instead of recording a wrong series.
 */
export class ObservedRestaurantOrderRepository
  implements RestaurantOrderRepository
{
  readonly #inner: RestaurantOrderRepository;
  readonly #metrics: RestaurantOrderDomainMetrics;

  constructor(
    inner: RestaurantOrderRepository,
    metrics: RestaurantOrderDomainMetrics,
  ) {
    this.#inner = inner;
    this.#metrics = metrics;
  }

  get(id: string): Promise<RestaurantOrder | undefined> {
    return this.#inner.get(id);
  }

  findByOrderId(orderId: string): Promise<RestaurantOrder | undefined> {
    return this.#inner.findByOrderId(orderId);
  }

  async save(order: RestaurantOrder): Promise<number> {
    const transitions = appliedTransitions(order.state, order.recordedEvents);
    const version = await this.#inner.save(order);
    this.#metrics.transitioned(transitions);
    return version;
  }
}

type RejectedTrigger = Readonly<{
  from: RestaurantOrderState;
  trigger: RestaurantOrderTrigger;
}>;

/**
 * Replaces `handler.execute` with a call that passes the result and the
 * error through unchanged and reports a rejected transition: an
 * InvalidTransitionError, or what `rejection` finds in the result. Returns
 * the same handler, so its type in the container does not change.
 */
export const observeTransitionRejections = <
  R,
  H extends { execute(command: never): Promise<R> },
>(
  handler: H,
  metrics: RestaurantOrderDomainMetrics,
  rejection?: (result: R) => RejectedTrigger | undefined,
): H => {
  const execute = handler.execute.bind(handler) as (
    command: unknown,
  ) => Promise<R>;
  Object.defineProperty(handler, "execute", {
    value: async (command: unknown): Promise<R> => {
      let result: R;
      try {
        result = await execute(command);
      } catch (error) {
        if (error instanceof InvalidTransitionError) {
          metrics.rejected(error.from, error.trigger);
        }
        throw error;
      }
      const rejected = rejection?.(result);
      if (rejected !== undefined) {
        metrics.rejected(rejected.from, rejected.trigger);
      }
      return result;
    },
    configurable: true,
    writable: true,
  });
  return handler;
};

/**
 * Cancel Restaurant Order answers a cancellation in a terminal state with a
 * business result instead of InvalidTransitionError: a rejected `Cancel`. A
 * rejection by the Preparation guard is not a missing transition.
 */
export const cancellationRejection = (
  result: CancelRestaurantOrderResult,
): RejectedTrigger | undefined =>
  result.outcome === "cancellation_rejected" && isTerminal(result.state)
    ? { from: result.state, trigger: RestaurantOrderTrigger.Cancel }
    : undefined;
