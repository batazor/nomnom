import type { Clock } from "../../../../pkg/usecase/clock.ts";
import { NotFoundError } from "../../../../pkg/usecase/errors.ts";
import {
  type AcceptingCatalogRestaurantView,
  acceptanceAt,
  type CatalogRestaurantView,
} from "../restaurant-view.ts";

export type GetRestaurant = Readonly<{ restaurantId: string }>;

/** Query model port: public data of one restaurant. */
export interface RestaurantPublicReader {
  get(restaurantId: string): Promise<CatalogRestaurantView | undefined>;
}

/**
 * Returns the public restaurant representation; public data needs no
 * authorization. Acceptance of orders is computed at the time of the read
 * from the flag and the schedule (FR-204).
 */
export class GetRestaurantHandler {
  readonly #reader: RestaurantPublicReader;
  readonly #clock: Clock;

  constructor(dependencies: { reader: RestaurantPublicReader; clock: Clock }) {
    this.#reader = dependencies.reader;
    this.#clock = dependencies.clock;
  }

  async execute(query: GetRestaurant): Promise<AcceptingCatalogRestaurantView> {
    const restaurant = await this.#reader.get(query.restaurantId);
    if (restaurant === undefined) {
      throw new NotFoundError("Restaurant", query.restaurantId);
    }
    return { ...restaurant, ...acceptanceAt(restaurant, this.#clock.now()) };
  }
}
