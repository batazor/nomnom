import type { Clock } from "../../../pkg/application/clock.ts";
import { PermissionDeniedError } from "../../../pkg/application/errors.ts";
import type { Outbox } from "../../../pkg/application/outbox.ts";
import type { Subject } from "../../../pkg/application/subject.ts";
import type { UnitOfWork } from "../../../pkg/application/unit-of-work.ts";
import type {
  Restaurant,
  RestaurantRepository,
} from "../domains/restaurant/restaurant.ts";
import type { RestaurantAccessControl } from "./access-control.ts";
import { loadRestaurant } from "./load-restaurant.ts";
import {
  type RestaurantChangeResult,
  toRestaurantView,
} from "./restaurant-view.ts";

export type ChangeOpeningDependencies = {
  accessControl: RestaurantAccessControl;
  unitOfWork: UnitOfWork;
  restaurants: RestaurantRepository;
  outbox: Outbox;
  clock: Clock;
};

/**
 * Shared flow of Open Restaurant and Close Restaurant: authorize, change the
 * opening in one Unit of Work and publish the recorded business fact.
 */
export const changeOpening = async (
  dependencies: ChangeOpeningDependencies,
  command: Readonly<{ subject: Subject; restaurantId: string }>,
  action: string,
  change: (restaurant: Restaurant, at: Date) => boolean,
): Promise<RestaurantChangeResult> => {
  if (
    !(await dependencies.accessControl.canManageRestaurant(
      command.subject,
      command.restaurantId,
    ))
  ) {
    throw new PermissionDeniedError(action);
  }
  const at = dependencies.clock.now();
  return dependencies.unitOfWork.run(async () => {
    const restaurant = await loadRestaurant(
      dependencies.restaurants,
      command.restaurantId,
    );
    if (!change(restaurant, at)) {
      return {
        ...toRestaurantView(restaurant),
        version: restaurant.version,
        changed: false,
      };
    }
    const version = await dependencies.restaurants.save(restaurant);
    await dependencies.outbox.add(
      { type: "Restaurant", id: restaurant.id, version },
      restaurant.pullEvents(),
    );
    return { ...toRestaurantView(restaurant), version, changed: true };
  });
};
