import type { MoneyValue } from "../../../menu/domains/menu/vo/money.ts";
import {
  OpeningHours,
  type OpeningInterval,
  type Restaurant,
  type ScheduledOrderSettingsValue,
  TimeZone,
} from "../../domains/restaurant/restaurant.ts";

/** Public representation of a restaurant returned by commands and queries. */
export type RestaurantView = Readonly<{
  restaurantId: string;
  name: string;
  /** Market of the restaurant (ADR 41). */
  marketId: string;
  /** IANA time zone of the restaurant (FR-201). */
  timeZone: string;
  /** Weekly opening hours on the wall clock (FR-696). */
  openingHours: readonly OpeningInterval[];
  /** Opened by Open Restaurant; accepting orders also needs the schedule. */
  isOpen: boolean;
  /** Fee of Premium Priority; absent when no premium is offered (FR-202). */
  premiumFee?: MoneyValue;
  /** Settings of scheduled orders; absent when none are taken (FR-672). */
  scheduledOrders?: ScheduledOrderSettingsValue;
}>;

/** Wait for a new order of the restaurant, computed when read (FR-645). */
export type RestaurantWaitView = Readonly<{
  /** Stored level of the Wait Time Statistics. */
  elevated: boolean;
  /** Current wait rounded up to a minute. */
  expectedWaitMinutes: number;
  /**
   * Baseline preparation time: the median of the sample, or the configured
   * preparation time until the sample is full. The preparation estimate of
   * a scheduled order.
   */
  baselinePreparationMs: number;
}>;

/** Public catalog representation: Get Restaurant and List Restaurants (FR-204). */
export type CatalogRestaurantView = RestaurantView &
  Readonly<{ waitTime: RestaurantWaitView }>;

/**
 * Acceptance of new orders at the time of the read (FR-204, FR-699): computed
 * from the stored flag and schedule, never cached.
 */
export type RestaurantAcceptanceView = Readonly<{
  acceptingOrders: boolean;
  /** Start of the next period; only when opened by the flag and outside the schedule. */
  nextOpeningAt?: Date;
}>;

export type AcceptingCatalogRestaurantView = CatalogRestaurantView &
  RestaurantAcceptanceView;

export const acceptanceAt = (
  view: RestaurantView,
  now: Date,
): RestaurantAcceptanceView => {
  if (!view.isOpen) {
    return { acceptingOrders: false };
  }
  const zone = TimeZone.of(view.timeZone);
  const hours = OpeningHours.of(view.openingHours);
  if (hours.periodAt(zone, now) !== undefined) {
    return { acceptingOrders: true };
  }
  const nextOpeningAt = hours.nextOpeningAfter(zone, now);
  return nextOpeningAt === undefined
    ? { acceptingOrders: false }
    : { acceptingOrders: false, nextOpeningAt };
};

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
  timeZone: restaurant.timeZone.value,
  openingHours: restaurant.openingHours.intervals,
  isOpen: restaurant.isOpen,
  ...(restaurant.premiumFee === undefined
    ? {}
    : { premiumFee: restaurant.premiumFee.toValue() }),
  ...(restaurant.scheduledOrders === undefined
    ? {}
    : { scheduledOrders: restaurant.scheduledOrders.toValue() }),
});
