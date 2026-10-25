// Business metrics of the Kitchen Order slice (domain and kitchen sections of
// ops/grafana/inventory; names, units and attributes from
// ops/semconv/model/domain.yaml and kitchen.yaml). Counters change only at an
// idempotently confirmed transition: by the Domain Events written to the
// Outbox, after the outermost Unit of Work commits. The domain does not know
// about metrics: the composition root decorates the Unit of Work, the Outbox
// and Receive Order Request with the types below.
import { AsyncLocalStorage } from "node:async_hooks";

import { type Counter, type Meter, metrics } from "@opentelemetry/api";
import type { MongoClient } from "mongodb";

import type {
  AggregateRef,
  Outbox,
  PublishableEvent,
} from "../../../../pkg/application/outbox.ts";
import type { UnitOfWork } from "../../../../pkg/application/unit-of-work.ts";
import { MongoUnitOfWork } from "../../../../pkg/infrastructure/mongodb/unit-of-work.ts";
import type {
  ReceiveOrderRequest,
  ReceiveOrderRequestHandler,
  ReceiveOrderRequestResult,
} from "../../applications/receive-order-request/receive-order-request.ts";
import type { RestaurantOrderEventType } from "../../domains/restaurantorder/restaurantorder.ts";
import { RESTAURANT_ORDER_AGGREGATE_TYPE } from "../integration/restaurant-order-events.ts";

export const DOMAIN_INSTRUMENTATION =
  "nomnom/restaurants/slices/restaurantorder/domain";

const REASON = "nomnom.reason";
/** Label of a reason outside the closed set, or of a missing reason. */
export const OTHER_REASON = "other";

/**
 * Closed sets of the `nomnom.reason` label. Reject Order and Report
 * Preparation Delay take an opaque reason (a catalog is not agreed yet), so
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

type Scope = { events: PublishableEvent[] };

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
    const scope: Scope = { events: [] };
    const result = await this.#scope.run(scope, () =>
      run(() => {
        scope.events = [];
        return work();
      }),
    );
    this.#record(scope.events);
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
 * {@link MongoUnitOfWork} of the processes: repositories, the Outbox and the
 * Inbox need its session, so the decorator keeps the concrete type.
 */
export class ObservedMongoUnitOfWork extends MongoUnitOfWork {
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
