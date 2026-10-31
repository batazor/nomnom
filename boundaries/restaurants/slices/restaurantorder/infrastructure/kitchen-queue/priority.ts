import { NotFoundError } from "../../../../pkg/usecase/errors.ts";
import type { WaitTimeStatisticsStore } from "../../../restaurant/applications/record-wait-time/record-wait-time.ts";
import type { WaitTimePolicy } from "../../../restaurant/domains/restaurant/wait-time.ts";
import type {
  KitchenQueueEntry,
  KitchenQueueOrdering,
} from "../../applications/kitchen-board/get-queue/get-queue.ts";
import { OrderPriority } from "../../domains/restaurantorder/restaurantorder.ts";

/**
 * Kitchen Queue policy (FR-241) in three classes:
 * 1. scheduled orders: premium before standard, then by Planned Ready At,
 *    `queued_at` and id — a scheduled order has a hard deadline before its
 *    Delivery Window, so it overtakes premium and aged orders;
 * 2. premium orders and standard ones waiting longer than the baseline wait
 *    of the restaurant times `agingFactor`;
 * 3. the other standard orders.
 * Classes 2 and 3 are FIFO by `queued_at`, then by id. The baseline comes
 * from the Wait Time Statistics, read in the active Unit of Work.
 */
export class PriorityKitchenQueueOrdering implements KitchenQueueOrdering {
  readonly #store: Pick<WaitTimeStatisticsStore, "load">;
  readonly #policy: WaitTimePolicy;
  readonly #agingFactor: number;

  constructor(dependencies: {
    store: Pick<WaitTimeStatisticsStore, "load">;
    policy: WaitTimePolicy;
    agingFactor: number;
  }) {
    if (
      !Number.isFinite(dependencies.agingFactor) ||
      dependencies.agingFactor <= 0
    ) {
      throw new Error("aging factor must be positive");
    }
    this.#store = dependencies.store;
    this.#policy = dependencies.policy;
    this.#agingFactor = dependencies.agingFactor;
  }

  async order(
    restaurantId: string,
    entries: readonly KitchenQueueEntry[],
    now: Date,
  ): Promise<readonly KitchenQueueEntry[]> {
    if (entries.length === 0) return entries;
    const stored = await this.#store.load(restaurantId);
    if (stored === undefined) {
      throw new NotFoundError("Restaurant", restaurantId);
    }
    const agingLimitMs =
      this.#policy.estimate(stored.statistics, now).baselineMs *
      this.#agingFactor;
    const queuedAt = (entry: KitchenQueueEntry): number => {
      if (entry.queuedAt === undefined) {
        throw new Error(
          `queued restaurant order ${entry.restaurantOrderId} has no queued_at`,
        );
      }
      return entry.queuedAt.getTime();
    };
    const premium = (entry: KitchenQueueEntry): number =>
      entry.priority === OrderPriority.Premium ? 0 : 1;
    const queueClass = (entry: KitchenQueueEntry): number => {
      if (entry.schedule !== undefined) return 0;
      return premium(entry) === 0 ||
        now.getTime() - queuedAt(entry) > agingLimitMs
        ? 1
        : 2;
    };
    const plannedReadyAt = (entry: KitchenQueueEntry): number =>
      entry.schedule === undefined
        ? 0
        : entry.schedule.plannedReadyAt.getTime();
    return [...entries].sort(
      (left, right) =>
        queueClass(left) - queueClass(right) ||
        (queueClass(left) === 0
          ? premium(left) - premium(right) ||
            plannedReadyAt(left) - plannedReadyAt(right)
          : 0) ||
        queuedAt(left) - queuedAt(right) ||
        (left.restaurantOrderId < right.restaurantOrderId ? -1 : 1),
    );
  }
}
