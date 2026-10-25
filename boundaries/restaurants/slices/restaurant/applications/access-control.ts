import type { Subject } from "../../../pkg/application/subject.ts";

/**
 * Authorization port of the `restaurant` slice. Methods express use-case needs;
 * the SpiceDB adapter maps them to permissions of `authorization/schema.zed`.
 * Implementations throw when the decision cannot be obtained (fail closed).
 */
export interface RestaurantAccessControl {
  canRegisterRestaurant(subject: Subject): Promise<boolean>;
  canManageRestaurant(subject: Subject, restaurantId: string): Promise<boolean>;
}

/**
 * Writes the creation-time `restaurant#owner` relationship. It is called with
 * a pre-generated restaurant id before the restaurant is stored; the write
 * MUST be idempotent (`TOUCH`).
 */
export interface OwnerRelationshipWriter {
  touchOwner(input: { restaurantId: string; ownerId: string }): Promise<void>;
}
