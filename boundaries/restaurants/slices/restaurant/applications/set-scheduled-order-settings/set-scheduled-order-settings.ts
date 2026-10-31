import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type {
  RestaurantRepository,
  ScheduledOrderSettingsValue,
} from "../../domains/restaurant/restaurant.ts";
import type { RestaurantAccessControl } from "../access-control/access-control.ts";
import { loadRestaurant } from "../load/load.ts";
import {
  type RestaurantChangeResult,
  toRestaurantView,
} from "../restaurant-view/restaurant-view.ts";

export type SetScheduledOrderSettings = Readonly<{
  subject: Subject;
  restaurantId: string;
  /**
   * Capacity per Delivery Window, opening lead and closing grace, set
   * together; `undefined` clears them and the restaurant stops taking
   * scheduled orders (FR-672).
   */
  settings: ScheduledOrderSettingsValue | undefined;
}>;

/**
 * Sets or clears the settings of scheduled orders of the restaurant. Existing
 * Slot Reservations are not affected; new ones follow the new settings. No
 * business fact is published, the catalog cache follows within its TTL.
 */
export class SetScheduledOrderSettingsHandler {
  readonly #accessControl: RestaurantAccessControl;
  readonly #restaurants: RestaurantRepository;

  constructor(dependencies: {
    accessControl: RestaurantAccessControl;
    restaurants: RestaurantRepository;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#restaurants = dependencies.restaurants;
  }

  async execute(
    command: SetScheduledOrderSettings,
  ): Promise<RestaurantChangeResult> {
    await requirePermission(
      this.#accessControl.canManageRestaurant(
        command.subject,
        command.restaurantId,
      ),
      "set scheduled order settings",
    );
    const restaurant = await loadRestaurant(
      this.#restaurants,
      command.restaurantId,
    );
    if (!restaurant.setScheduledOrderSettings(command.settings)) {
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
