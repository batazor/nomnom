import { NotFoundError } from "../../../../pkg/application/errors.ts";
import type { CatalogRestaurantView } from "../restaurant-view.ts";

export type GetRestaurant = Readonly<{ restaurantId: string }>;

/** Query model port: public data of one restaurant. */
export interface RestaurantPublicReader {
  get(restaurantId: string): Promise<CatalogRestaurantView | undefined>;
}

/** Returns the public restaurant representation; public data needs no authorization. */
export class GetRestaurantHandler {
  readonly #reader: RestaurantPublicReader;

  constructor(dependencies: { reader: RestaurantPublicReader }) {
    this.#reader = dependencies.reader;
  }

  async execute(query: GetRestaurant): Promise<CatalogRestaurantView> {
    const restaurant = await this.#reader.get(query.restaurantId);
    if (restaurant === undefined) {
      throw new NotFoundError("Restaurant", query.restaurantId);
    }
    return restaurant;
  }
}
