import type { Outbox } from "../../../../pkg/outbox/outbox.ts";
import type { UnitOfWork } from "../../../../pkg/unit-of-work/unit-of-work.ts";
import type {
  WaitLevelChanged,
  WaitTimePolicy,
  WaitTimeStatistics,
} from "../../domains/restaurant/wait-time.ts";
import type { Clock } from "../clock/clock.ts";
import { NotFoundError } from "../failure/failure.ts";

/** `aggregate_type` of RestaurantWaitLevelChanged in the Outbox. */
export const WAIT_TIME_AGGREGATE_TYPE = "RestaurantWaitTime";

export type StoredWaitTime = Readonly<{
  marketId: string;
  statistics: WaitTimeStatistics;
}>;

/**
 * Wait Time Statistics kept in the restaurant document. Writes do not change
 * the version of the Restaurant aggregate; both join the active Unit of Work.
 */
export interface WaitTimeStatisticsStore {
  load(restaurantId: string): Promise<StoredWaitTime | undefined>;
  save(restaurantId: string, statistics: WaitTimeStatistics): Promise<void>;
}

/**
 * Restaurant Orders that became ready or were dropped. A redriven fact arrives
 * after newer ones (ADR 11, ADR 28): the record keeps its `queued` from opening
 * an order nothing would close. Writes join the active Unit of Work; records
 * expire with the DLQ retention.
 */
export interface ClosedPreparations {
  has(orderId: string): Promise<boolean>;
  add(orderId: string, closedAt: Date): Promise<void>;
}

/**
 * A fact of a Restaurant Order that changes the statistics: `queued` at the
 * moment the order joined the Kitchen Queue, `ready` at the moment it became
 * ready, `dropped` when it was cancelled or failed. `factAt` is when the fact
 * was published: the level is the one at that moment, so a backlog of facts
 * does not read as a long wait.
 */
export type RecordWaitTime = Readonly<{
  restaurantId: string;
  orderId: string;
  fact: "queued" | "ready" | "dropped";
  occurredAt: Date;
  factAt: Date;
}>;

/**
 * Applies a Restaurant Order fact to the Wait Time Statistics of its
 * restaurant and, when the level changes, publishes RestaurantWaitLevelChanged
 * in the same Unit of Work (FR-644, FR-646). A repeated fact changes nothing.
 */
export class RecordWaitTimeHandler {
  readonly #unitOfWork: UnitOfWork;
  readonly #store: WaitTimeStatisticsStore;
  readonly #closed: ClosedPreparations;
  readonly #policy: WaitTimePolicy;
  readonly #outbox: Outbox;
  readonly #clock: Clock;

  constructor(dependencies: {
    unitOfWork: UnitOfWork;
    store: WaitTimeStatisticsStore;
    closed: ClosedPreparations;
    policy: WaitTimePolicy;
    outbox: Outbox;
    clock: Clock;
  }) {
    this.#unitOfWork = dependencies.unitOfWork;
    this.#store = dependencies.store;
    this.#closed = dependencies.closed;
    this.#policy = dependencies.policy;
    this.#outbox = dependencies.outbox;
    this.#clock = dependencies.clock;
  }

  execute(command: RecordWaitTime): Promise<void> {
    return this.#unitOfWork.run(async () => {
      const stored = await this.#store.load(command.restaurantId);
      if (stored === undefined) {
        throw new NotFoundError("Restaurant", command.restaurantId);
      }
      if (command.fact !== "queued") {
        await this.#closed.add(command.orderId, this.#clock.now());
      } else if (await this.#closed.has(command.orderId)) {
        return;
      }
      const current = stored.statistics;
      const recorded = this.#apply(current, command);
      if (recorded === current) return;
      const level = this.#policy.level(recorded, command.factAt);
      if (level === current.level) {
        await this.#store.save(command.restaurantId, recorded);
        return;
      }
      const statistics = { ...recorded, level, revision: current.revision + 1 };
      await this.#store.save(command.restaurantId, statistics);
      const event: WaitLevelChanged = {
        type: "RestaurantWaitLevelChanged",
        restaurantId: command.restaurantId,
        marketId: stored.marketId,
        elevated: level === "ELEVATED",
        occurredAt: this.#clock.now(),
      };
      await this.#outbox.add(
        {
          type: WAIT_TIME_AGGREGATE_TYPE,
          id: command.restaurantId,
          version: statistics.revision,
        },
        [event],
      );
    });
  }

  #apply(
    statistics: WaitTimeStatistics,
    command: RecordWaitTime,
  ): WaitTimeStatistics {
    switch (command.fact) {
      case "queued":
        return this.#policy.queued(
          statistics,
          command.orderId,
          command.occurredAt,
        );
      case "ready":
        return this.#policy.ready(
          statistics,
          command.orderId,
          command.occurredAt,
        );
      case "dropped":
        return this.#policy.dropped(statistics, command.orderId);
    }
  }
}
