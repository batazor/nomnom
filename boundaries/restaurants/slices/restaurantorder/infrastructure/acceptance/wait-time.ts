import { NotFoundError } from "../../../../pkg/usecase/errors.ts";
import type { WaitTimeStatisticsStore } from "../../../restaurant/applications/record-wait-time/record-wait-time.ts";
import type { WaitTimePolicy } from "../../../restaurant/domains/restaurant/wait-time.ts";
import type { ReadyTimeEstimator } from "../../applications/intake/accept-order/accept-order.ts";

/**
 * Initial `expected_ready_at` from the Wait Time Statistics of the restaurant
 * (FR-609): Acceptance plus the current wait. Reads the restaurant document
 * of the `restaurant` slice in the active Unit of Work; already accepted
 * orders keep their time.
 */
export class WaitTimeReadyTimeEstimator implements ReadyTimeEstimator {
  readonly #store: Pick<WaitTimeStatisticsStore, "load">;
  readonly #policy: WaitTimePolicy;

  constructor(dependencies: {
    store: Pick<WaitTimeStatisticsStore, "load">;
    policy: WaitTimePolicy;
  }) {
    this.#store = dependencies.store;
    this.#policy = dependencies.policy;
  }

  async estimate(input: {
    restaurantId: string;
    acceptedAt: Date;
  }): Promise<Date> {
    const stored = await this.#store.load(input.restaurantId);
    if (stored === undefined) {
      throw new NotFoundError("Restaurant", input.restaurantId);
    }
    const { currentMs } = this.#policy.estimate(
      stored.statistics,
      input.acceptedAt,
    );
    return new Date(input.acceptedAt.getTime() + currentMs);
  }
}
