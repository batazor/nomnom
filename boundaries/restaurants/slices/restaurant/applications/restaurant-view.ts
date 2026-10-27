import type { MoneyValue } from "../../menu/domains/menu/vo/money.ts";
import type { Restaurant } from "../domains/restaurant/restaurant.ts";

/** Public representation of a restaurant returned by commands and queries. */
export type RestaurantView = Readonly<{
  restaurantId: string;
  name: string;
  /** Market of the restaurant (ADR 41). */
  marketId: string;
  isOpen: boolean;
  /** Fee of Premium Priority; absent when no premium is offered (FR-202). */
  premiumFee?: MoneyValue;
}>;

/** Wait for a new order of the restaurant, computed when read (FR-645). */
export type RestaurantWaitView = Readonly<{
  /** Stored level of the Wait Time Statistics. */
  elevated: boolean;
  /** Current wait rounded up to a minute. */
  expectedWaitMinutes: number;
}>;

/** Public catalog representation: Get Restaurant and List Restaurants (FR-204). */
export type CatalogRestaurantView = RestaurantView &
  Readonly<{ waitTime: RestaurantWaitView }>;

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
  ...(restaurant.premiumFee === undefined
    ? {}
    : { premiumFee: restaurant.premiumFee.toValue() }),
});
