import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { Clock } from "../../../../pkg/usecase/clock.ts";
import type { RestaurantRepository } from "../../domains/restaurant/restaurant.ts";
import type { MoneyValue } from "../../domains/restaurant/vo/money.ts";
import type { RestaurantAccessControl } from "../access-control/access-control.ts";
import { loadRestaurant } from "../load-restaurant/load-restaurant.ts";
import {
  type RestaurantChangeResult,
  toRestaurantView,
} from "../restaurant-view/restaurant-view.ts";

/** What Update Restaurant does with the premium fee (FR-202). */
export type PremiumFeeChange =
  | Readonly<{ kind: "keep" }>
  | Readonly<{ kind: "set"; fee: MoneyValue }>
  | Readonly<{ kind: "clear" }>;

export type UpdateRestaurant = Readonly<{
  subject: Subject;
  restaurantId: string;
  name: string;
  premiumFee: PremiumFeeChange;
}>;

/**
 * Changes the name and the premium fee of the restaurant; no business fact is
 * published.
 */
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
    const renamed = restaurant.rename(command.name, this.#clock.now());
    const feeChanged =
      command.premiumFee.kind === "keep"
        ? false
        : restaurant.changePremiumFee(
            command.premiumFee.kind === "set"
              ? command.premiumFee.fee
              : undefined,
          );
    if (!renamed && !feeChanged) {
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
