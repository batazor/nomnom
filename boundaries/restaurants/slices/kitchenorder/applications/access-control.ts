import type { Subject } from "../../../pkg/application/subject.ts";

/**
 * Authorization port of the `kitchenorder` slice over `restaurant:<id>`.
 * Implementations throw when no decision can be obtained (fail closed).
 */
export interface KitchenAccessControl {
  /** Kitchen staff of the restaurant (relation `staff`). */
  canOperateKitchen(subject: Subject, restaurantId: string): Promise<boolean>;
  /** `service` identity of the `orders` fulfillment process. */
  canCoordinateFulfillment(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean>;
  /** Staff or an authorized internal actor reading Restaurant Orders. */
  canViewRestaurantOrders(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean>;
}
