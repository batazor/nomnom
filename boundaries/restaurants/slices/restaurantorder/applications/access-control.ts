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

/**
 * `service` identity of the `orders` fulfillment process for commands that
 * address only a Restaurant Order: the platform capability is checked before
 * the order is read.
 */
export interface FulfillmentProcessAccess {
  canCoordinateFulfillment(subject: Subject): Promise<boolean>;
}

/** Staff or an authorized internal actor reading Restaurant Orders. */
export interface RestaurantOrderViewAccess {
  canViewRestaurantOrders(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean>;
}

/** Source of Pickup facts: the `service` identity behind `delivery` events. */
export interface PickupWitnessAccess {
  canRecordPickup(subject: Subject): Promise<boolean>;
}
