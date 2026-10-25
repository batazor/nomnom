import type { Subject } from "../../../pkg/application/subject.ts";

// Authorization ports of the `restaurantorder` slice over `restaurant:<id>`,
// one per kind of caller; each use case depends only on the one it checks.
// Implementations throw when no decision can be obtained (fail closed).

/** Kitchen staff of the restaurant (relation `staff`). */
export interface KitchenStaffAccess {
  canOperateKitchen(subject: Subject, restaurantId: string): Promise<boolean>;
}

/** `service` identity of the `orders` fulfillment process. */
export interface FulfillmentCoordinatorAccess {
  canCoordinateFulfillment(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean>;
}

/** Staff or an authorized internal actor reading Restaurant Orders. */
export interface RestaurantOrderViewAccess {
  canViewRestaurantOrders(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean>;
}
