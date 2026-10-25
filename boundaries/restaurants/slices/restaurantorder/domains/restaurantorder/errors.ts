export const InvalidRestaurantOrderReason = {
  EmptyItems: "empty_items",
  EmptyMenuItemId: "empty_menu_item_id",
  InvalidQuantity: "invalid_quantity",
  DuplicateItem: "duplicate_item",
  EmptyFailureReason: "empty_failure_reason",
} as const;

export type InvalidRestaurantOrderReason =
  (typeof InvalidRestaurantOrderReason)[keyof typeof InvalidRestaurantOrderReason];

export class InvalidRestaurantOrderError extends Error {
  readonly code = "invalid_restaurant_order";
  readonly reason: InvalidRestaurantOrderReason;

  constructor(reason: InvalidRestaurantOrderReason) {
    super(`invalid restaurant order: ${reason}`);
    this.name = "InvalidRestaurantOrderError";
    this.reason = reason;
  }
}

/** A Restaurant Order already exists for the contract `orderId`. */
export class DuplicateOrderIdError extends Error {
  readonly code = "duplicate_order_id";
  readonly orderId: string;

  constructor(orderId: string) {
    super(`a restaurant order for order ${orderId} already exists`);
    this.name = "DuplicateOrderIdError";
    this.orderId = orderId;
  }
}
