import type { Restaurant } from "../domains/restaurant/restaurant.ts";

/** Public representation of a restaurant returned by commands and queries. */
export type RestaurantView = Readonly<{
  restaurantId: string;
  name: string;
  /** Market of the restaurant (ADR 41). */
  marketId: string;
  isOpen: boolean;
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
  marketId: restaurant.marketId.value,
  isOpen: restaurant.isOpen,
});
