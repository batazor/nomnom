import type { Clock } from "../../../../pkg/application/clock.ts";
import { NotFoundError } from "../../../../pkg/application/errors.ts";
import type { RestaurantRepository } from "../../../restaurant/domains/restaurant/restaurant.ts";
import type {
  DeliveryWindows,
  ScheduleSlotRepository,
} from "../../domains/scheduleslot/scheduleslot.ts";
import {
  type WindowAvailability,
  windowAvailability,
} from "../window-availability.ts";

export type ListScheduledSlots = Readonly<{ restaurantId: string }>;

/**
 * Delivery Windows of a restaurant with their availability (FR-673, FR-703):
 * the UTC-aligned grid from now + MIN_LEAD to now + MAX_HORIZON inside the
 * schedule periods. Empty for a restaurant without settings of scheduled
 * orders or not accepting orders now. Public, like Get Restaurant.
 */
export class ListScheduledSlotsHandler {
  readonly #restaurants: Pick<RestaurantRepository, "get">;
  readonly #slots: Pick<ScheduleSlotRepository, "listBetween">;
  readonly #windows: DeliveryWindows;
  readonly #clock: Clock;

  constructor(dependencies: {
    restaurants: Pick<RestaurantRepository, "get">;
    slots: Pick<ScheduleSlotRepository, "listBetween">;
    windows: DeliveryWindows;
    clock: Clock;
  }) {
    this.#restaurants = dependencies.restaurants;
    this.#slots = dependencies.slots;
    this.#windows = dependencies.windows;
    this.#clock = dependencies.clock;
  }

  async execute(
    query: ListScheduledSlots,
  ): Promise<readonly WindowAvailability[]> {
    const restaurant = await this.#restaurants.get(query.restaurantId);
    if (restaurant === undefined) {
      throw new NotFoundError("Restaurant", query.restaurantId);
    }
    return windowAvailability(
      { windows: this.#windows, slots: this.#slots },
      restaurant,
      this.#clock.now(),
    );
  }
}
