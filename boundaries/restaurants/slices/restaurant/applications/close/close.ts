import type { Subject } from "../../../../pkg/authz/subject.ts";
import {
  type ChangeOpeningDependencies,
  changeOpening,
} from "../change-opening/change-opening.ts";
import type { RestaurantChangeResult } from "../restaurant-view/restaurant-view.ts";

export type CloseRestaurant = Readonly<{
  subject: Subject;
  restaurantId: string;
}>;

/**
 * Closes the restaurant for new Restaurant Orders and publishes
 * `RestaurantClosed`; already received or accepted orders are not cancelled.
 */
export class CloseRestaurantHandler {
  readonly #dependencies: ChangeOpeningDependencies;

  constructor(dependencies: ChangeOpeningDependencies) {
    this.#dependencies = dependencies;
  }

  execute(command: CloseRestaurant): Promise<RestaurantChangeResult> {
    return changeOpening(
      this.#dependencies,
      command,
      "close restaurant",
      (restaurant, at) => restaurant.close(at),
    );
  }
}
