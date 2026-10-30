import type { Subject } from "../../../../pkg/authz/subject.ts";

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
 * Revision (ZedToken) of a relationship write: opaque string returned by
 * SpiceDB. It is stored with the restaurant so later checks of that
 * restaurant use `at_least_as_fresh` (FR-447).
 */
export type AuthorizationRevision = string;

/**
 * Writes the creation-time `restaurant#owner` relationship. It is called with
 * a pre-generated restaurant id before the restaurant is stored; the write
 * MUST be idempotent (`TOUCH`). Returns the revision of the write.
 */
export interface OwnerRelationshipWriter {
  touchOwner(input: {
    restaurantId: string;
    ownerId: string;
  }): Promise<AuthorizationRevision>;
}

/**
 * Writes `restaurant#staff` relationships of Assign Staff (`TOUCH`) and Revoke
 * Staff (`DELETE`). Both are called before the staff change is stored and
 * MUST be idempotent; an error aborts the use case (fail closed). Both return
 * the revision of the write.
 */
export interface StaffRelationshipWriter {
  touchStaff(input: {
    restaurantId: string;
    userId: string;
  }): Promise<AuthorizationRevision>;
  deleteStaff(input: {
    restaurantId: string;
    userId: string;
  }): Promise<AuthorizationRevision>;
}
