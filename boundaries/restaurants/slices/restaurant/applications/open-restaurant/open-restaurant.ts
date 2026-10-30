import type { Subject } from "../../../../pkg/authz/subject.ts";
import {
  type ChangeOpeningDependencies,
  changeOpening,
} from "../change-opening/change-opening.ts";
import type { RestaurantChangeResult } from "../restaurant-view/restaurant-view.ts";

export type OpenRestaurant = Readonly<{
  subject: Subject;
  restaurantId: string;
}>;

/** Opens the restaurant for new Restaurant Orders and publishes `RestaurantOpened`. */
export class OpenRestaurantHandler {
  readonly #dependencies: ChangeOpeningDependencies;

  constructor(dependencies: ChangeOpeningDependencies) {
    this.#dependencies = dependencies;
  }

  execute(command: OpenRestaurant): Promise<RestaurantChangeResult> {
    return changeOpening(
      this.#dependencies,
      command,
      "open restaurant",
      (restaurant, at) => restaurant.open(at),
    );
  }
}
