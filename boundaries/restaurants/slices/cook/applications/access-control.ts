import type { Subject } from "../../../pkg/application/subject.ts";

// Authorization port of the `cook` slice over `restaurant:<id>`.
// Implementations throw when no decision can be obtained (fail closed).

export interface CookPoolAccess {
  /** `restaurant#manage_cooks`: hire, line and retirement of cooks. */
  canManageCooks(subject: Subject, restaurantId: string): Promise<boolean>;
  /** `restaurant#view_orders`: the Cook Pool and its load. */
  canViewRestaurantOrders(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean>;
}
