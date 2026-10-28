import { requirePermission } from "../../../../pkg/application/require-permission.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import type {
  OpeningInterval,
  RestaurantRepository,
} from "../../domains/restaurant/restaurant.ts";
import type { RestaurantAccessControl } from "../access-control.ts";
import { loadRestaurant } from "../load-restaurant.ts";
import {
  type RestaurantChangeResult,
  toRestaurantView,
} from "../restaurant-view.ts";

export type SetOpeningHours = Readonly<{
  subject: Subject;
  restaurantId: string;
  /** The whole weekly schedule; it replaces the stored one (FR-696). */
  openingHours: readonly OpeningInterval[];
}>;

/**
 * Replaces the weekly opening hours of the restaurant; no business fact is
 * published, the catalog cache follows within its TTL.
 */
export class SetOpeningHoursHandler {
  readonly #accessControl: RestaurantAccessControl;
  readonly #restaurants: RestaurantRepository;

  constructor(dependencies: {
    accessControl: RestaurantAccessControl;
    restaurants: RestaurantRepository;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#restaurants = dependencies.restaurants;
  }

  async execute(command: SetOpeningHours): Promise<RestaurantChangeResult> {
    await requirePermission(
      this.#accessControl.canManageRestaurant(
        command.subject,
        command.restaurantId,
      ),
      "set opening hours",
    );
    const restaurant = await loadRestaurant(
      this.#restaurants,
      command.restaurantId,
    );
    if (!restaurant.setOpeningHours(command.openingHours)) {
      return {
        ...toRestaurantView(restaurant),
        version: restaurant.version,
        changed: false,
      };
    }
    const version = await this.#restaurants.save(restaurant);
    restaurant.pullEvents();
    return { ...toRestaurantView(restaurant), version, changed: true };
  }
}
