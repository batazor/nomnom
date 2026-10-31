import type { Clock } from "../clock/clock.ts";
import {
  type AcceptingCatalogRestaurantView,
  acceptanceAt,
  type CatalogRestaurantView,
} from "../restaurant-view/restaurant-view.ts";

export type BatchGetRestaurants = Readonly<{
  restaurantIds: readonly string[];
}>;

/** Query model port: public data of many restaurants in one read. */
export interface RestaurantBatchReader {
  /** Found restaurants of the distinct `restaurantIds`, in no particular order. */
  getMany(
    restaurantIds: readonly string[],
  ): Promise<readonly CatalogRestaurantView[]>;
}

const MAX_BATCH_SIZE = 100;

export class InvalidBatchError extends Error {
  readonly code = "invalid_batch";

  constructor(size: number) {
    super(`a batch must have 1..${MAX_BATCH_SIZE} ids, got ${size}`);
    this.name = "InvalidBatchError";
  }
}

/** The distinct ids of a batch; empty or more than 100 is InvalidBatchError. */
const batchIds = (ids: readonly string[]): readonly string[] => {
  if (ids.length === 0 || ids.length > MAX_BATCH_SIZE) {
    throw new InvalidBatchError(ids.length);
  }
  return [...new Set(ids)];
};

/**
 * Returns the public representation of up to 100 restaurants for a page of
 * the caller (ADR 7). An unknown id is absent from the result; public data
 * needs no authorization.
 */
export class BatchGetRestaurantsHandler {
  readonly #reader: RestaurantBatchReader;
  readonly #clock: Clock;

  constructor(dependencies: { reader: RestaurantBatchReader; clock: Clock }) {
    this.#reader = dependencies.reader;
    this.#clock = dependencies.clock;
  }

  async execute(
    query: BatchGetRestaurants,
  ): Promise<readonly AcceptingCatalogRestaurantView[]> {
    const now = this.#clock.now();
    const restaurants = await this.#reader.getMany(
      batchIds(query.restaurantIds),
    );
    return restaurants.map((restaurant) => ({
      ...restaurant,
      ...acceptanceAt(restaurant, now),
    }));
  }
}
