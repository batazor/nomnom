import type { RestaurantRepository } from "../../restaurant/domains/restaurant/restaurant.ts";
import type { RestaurantOwners } from "../applications/simulate-kitchen/simulate-kitchen.ts";

/** Owner of a restaurant read from the Restaurant aggregate. */
export class RepositoryRestaurantOwners implements RestaurantOwners {
  readonly #restaurants: RestaurantRepository;

  constructor(restaurants: RestaurantRepository) {
    this.#restaurants = restaurants;
  }

  async ownerOf(restaurantId: string): Promise<string | undefined> {
    return (await this.#restaurants.get(restaurantId))?.ownerId;
  }
}
