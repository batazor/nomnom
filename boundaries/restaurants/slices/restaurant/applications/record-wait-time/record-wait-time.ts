import type { Clock } from "../../../../pkg/application/clock.ts";
import { NotFoundError } from "../../../../pkg/application/errors.ts";
import type { Outbox } from "../../../../pkg/application/outbox.ts";
import type { UnitOfWork } from "../../../../pkg/application/unit-of-work.ts";
import type {
  WaitLevelChanged,
  WaitTimePolicy,
  WaitTimeStatistics,
} from "../../domains/restaurant/wait-time.ts";

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

/** A fact of a Restaurant Order that changes the statistics. */
export type RecordWaitTime = Readonly<{
  restaurantId: string;
  orderId: string;
  fact: "accepted" | "ready" | "dropped";
  occurredAt: Date;
}>;

/**
 * Applies a Restaurant Order fact to the Wait Time Statistics of its
 * restaurant and, when the level changes, publishes RestaurantWaitLevelChanged
 * in the same Unit of Work (FR-644, FR-646). A repeated fact changes nothing.
 */
export class RecordWaitTimeHandler {
  readonly #unitOfWork: UnitOfWork;
  readonly #store: WaitTimeStatisticsStore;
  readonly #policy: WaitTimePolicy;
  readonly #outbox: Outbox;
  readonly #clock: Clock;

  constructor(dependencies: {
    unitOfWork: UnitOfWork;
    store: WaitTimeStatisticsStore;
    policy: WaitTimePolicy;
    outbox: Outbox;
    clock: Clock;
  }) {
    this.#unitOfWork = dependencies.unitOfWork;
    this.#store = dependencies.store;
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
      const current = stored.statistics;
      const recorded = this.#apply(current, command);
      if (recorded === current) return;
      const now = this.#clock.now();
      const level = this.#policy.level(recorded, now);
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
        occurredAt: now,
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
      case "accepted":
        return this.#policy.accepted(
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
