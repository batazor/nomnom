import type { RestaurantRepository } from "../../../restaurant/domains/restaurant/restaurant.ts";
import type { AcceptanceConditionsProvider } from "../../applications/accept-order/accept-order.ts";
import type { AcceptanceConditions } from "../../domains/restaurantorder/acceptance.ts";

const BEGINNING = new Date(0);
const END = new Date(8.64e15);

/**
 * Acceptance conditions from the opening hours of the restaurant (FR-699):
 * the bounds of the schedule period running at `now`. Outside every period
 * the bounds are an empty period at `now`, so `RestaurantAcceptingOrders`
 * reports `outside_opening_hours`. Capacity and item windows have no model
 * yet and do not constrain. Reads the restaurant through its repository, so
 * it joins the active Unit of Work and makes no external call.
 */
export class OpeningHoursAcceptanceConditions
  implements AcceptanceConditionsProvider
{
  readonly #restaurants: Pick<RestaurantRepository, "get">;

  constructor(restaurants: Pick<RestaurantRepository, "get">) {
    this.#restaurants = restaurants;
  }

  async load(input: {
    restaurantId: string;
    menuItemIds: readonly string[];
    now: Date;
  }): Promise<AcceptanceConditions> {
    const restaurant = await this.#restaurants.get(input.restaurantId);
    if (restaurant === undefined) {
      throw new Error(`restaurant ${input.restaurantId} disappeared`);
    }
    const period = restaurant.openingPeriodAt(input.now);
    const menuItems: Record<string, AcceptanceConditions["menuItems"][string]> =
      {};
    for (const menuItemId of input.menuItemIds) {
      menuItems[menuItemId] = {
        availableFrom: BEGINNING,
        availableUntil: END,
        remainingCapacity: Number.MAX_SAFE_INTEGER,
      };
    }
    return {
      restaurant: {
        opensAt: period?.opensAt ?? input.now,
        closesAt: period?.closesAt ?? input.now,
        currentOrders: 0,
        maximumOrders: Number.MAX_SAFE_INTEGER,
        scheduleException: false,
      },
      menuItems,
    };
  }
}
