import { requirePermission } from "../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../pkg/authz/subject.ts";
import type {
  Restaurant,
  RestaurantRepository,
} from "../domains/restaurant/restaurant.ts";
import type {
  AuthorizationRevision,
  RestaurantAccessControl,
  StaffRelationshipWriter,
} from "./access-control.ts";
import { loadRestaurant } from "./load-restaurant.ts";

export type ChangeStaffDependencies = {
  accessControl: RestaurantAccessControl;
  staffRelationships: StaffRelationshipWriter;
  restaurants: RestaurantRepository;
};

export type ChangeStaff = Readonly<{
  subject: Subject;
  restaurantId: string;
  userId: string;
}>;

/** Result of Assign Staff and Revoke Staff. */
export type StaffChangeResult = Readonly<{
  restaurantId: string;
  /** User ids of the staff after the command, sorted. */
  staff: readonly string[];
  version: number;
  changed: boolean;
}>;

/**
 * Shared flow of Assign Staff and Revoke Staff: require `restaurant#manage`,
 * change the staff set and, only on a real change, write the
 * `restaurant#staff` relationship before the restaurant is saved (dual-write
 * rule: an error of the write aborts the command, an orphan left by a failed
 * save is removed by reconciliation). The revision of the write is stored
 * with the restaurant. A repeat is a no-op without a write and keeps the
 * stored revision.
 */
export const changeStaff = async (
  dependencies: ChangeStaffDependencies,
  command: ChangeStaff,
  action: string,
  change: (restaurant: Restaurant, userId: string) => boolean,
  write: (
    writer: StaffRelationshipWriter,
    input: { restaurantId: string; userId: string },
  ) => Promise<AuthorizationRevision>,
): Promise<StaffChangeResult> => {
  await requirePermission(
    dependencies.accessControl.canManageRestaurant(
      command.subject,
      command.restaurantId,
    ),
    action,
  );
  const restaurant = await loadRestaurant(
    dependencies.restaurants,
    command.restaurantId,
  );
  if (!change(restaurant, command.userId)) {
    return {
      restaurantId: restaurant.id,
      staff: restaurant.staff,
      version: restaurant.version,
      changed: false,
    };
  }
  const revision = await write(dependencies.staffRelationships, {
    restaurantId: restaurant.id,
    userId: command.userId.trim(),
  });
  // The ZedToken is saved with the staff change, so the next check of this
  // restaurant sees the write (`at_least_as_fresh`, FR-447).
  restaurant.recordAuthorizationRevision(revision);
  const version = await dependencies.restaurants.save(restaurant);
  return {
    restaurantId: restaurant.id,
    staff: restaurant.staff,
    version,
    changed: true,
  };
};
