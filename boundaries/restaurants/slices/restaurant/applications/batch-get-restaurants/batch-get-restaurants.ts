import { batchIds } from "../../../../pkg/application/batch.ts";
import type { CatalogRestaurantView } from "../restaurant-view.ts";

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

  constructor(dependencies: { reader: RestaurantBatchReader }) {
    this.#reader = dependencies.reader;
  }

  async execute(
    query: BatchGetRestaurants,
  ): Promise<readonly CatalogRestaurantView[]> {
    return this.#reader.getMany(batchIds(query.restaurantIds));
  }
}
