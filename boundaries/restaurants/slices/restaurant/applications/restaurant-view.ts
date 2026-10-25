import type { Restaurant } from "../domains/restaurant/restaurant.ts";

/** Public representation of a restaurant returned by commands and queries. */
export type RestaurantView = Readonly<{
  restaurantId: string;
  name: string;
  isOpen: boolean;
  /** Pickup address; empty for a restaurant registered before it was required. */
  address: string;
}>;

/** Result of commands that change a restaurant. */
export type RestaurantChangeResult = RestaurantView &
  Readonly<{
    version: number;
    changed: boolean;
  }>;

export const toRestaurantView = (restaurant: Restaurant): RestaurantView => ({
  restaurantId: restaurant.id,
  name: restaurant.name,
  isOpen: restaurant.isOpen,
  address: restaurant.address,
});
