import type { Clock } from "../../../../pkg/application/clock.ts";
import { requirePermission } from "../../../../pkg/application/require-permission.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import type { RestaurantRepository } from "../../domains/restaurant/restaurant.ts";
import type { RestaurantAccessControl } from "../access-control.ts";
import { loadRestaurant } from "../load-restaurant.ts";
import {
  type RestaurantChangeResult,
  toRestaurantView,
} from "../restaurant-view.ts";

export type UpdateRestaurant = Readonly<{
  subject: Subject;
  restaurantId: string;
  name: string;
  /** New pickup address; omitted keeps the current one. */
  address?: string;
}>;

/** Changes the basic restaurant data; no business fact is published. */
export class UpdateRestaurantHandler {
  readonly #accessControl: RestaurantAccessControl;
  readonly #restaurants: RestaurantRepository;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: RestaurantAccessControl;
    restaurants: RestaurantRepository;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#restaurants = dependencies.restaurants;
    this.#clock = dependencies.clock;
  }

  async execute(command: UpdateRestaurant): Promise<RestaurantChangeResult> {
    await requirePermission(
      this.#accessControl.canManageRestaurant(
        command.subject,
        command.restaurantId,
      ),
      "update restaurant",
    );
    const restaurant = await loadRestaurant(
      this.#restaurants,
      command.restaurantId,
    );
    const now = this.#clock.now();
    const renamed = restaurant.rename(command.name, now);
    const relocated =
      command.address !== undefined &&
      restaurant.changeAddress(command.address, now);
    if (!renamed && !relocated) {
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
