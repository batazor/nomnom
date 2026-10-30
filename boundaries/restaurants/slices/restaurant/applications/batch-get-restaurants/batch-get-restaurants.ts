import { batchIds } from "../../../../pkg/usecase/batch.ts";
import type { Clock } from "../../../../pkg/usecase/clock.ts";
import {
  type AcceptingCatalogRestaurantView,
  acceptanceAt,
  type CatalogRestaurantView,
} from "../restaurant-view.ts";

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
