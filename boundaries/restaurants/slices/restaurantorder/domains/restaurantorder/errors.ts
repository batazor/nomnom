export const InvalidRestaurantOrderReason = {
  EmptyItems: "empty_items",
  EmptyMenuItemId: "empty_menu_item_id",
  InvalidQuantity: "invalid_quantity",
  DuplicateItem: "duplicate_item",
  EmptyFailureReason: "empty_failure_reason",
  ExpectedReadyAtNotLater: "expected_ready_at_not_later",
  EmptyCookId: "empty_cook_id",
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

/**
 * Start Preparation found no free cook on line in the Cook Pool of the
 * restaurant; the order stays in `QUEUED` (FR-225).
 */
export class NoCookAvailableError extends Error {
  readonly code = "no_cook_available";
  readonly restaurantId: string;

  constructor(restaurantId: string) {
    super(`restaurant ${restaurantId} has no free cook on line`);
    this.name = "NoCookAvailableError";
    this.restaurantId = restaurantId;
  }
}

/**
 * The Market a Receive Order Request names differs from the Market of the
 * restaurant (or of the already received order): a Restaurant Order never
 * crosses a Market boundary (ADR 41), and neither Market is picked silently.
 */
export class MarketMismatchError extends Error {
  readonly code = "market_mismatch";
  readonly orderId: string;
  readonly expectedMarketId: string;
  readonly requestedMarketId: string;

  constructor(input: {
    orderId: string;
    expectedMarketId: string;
    requestedMarketId: string;
  }) {
    super(
      `order ${input.orderId} names market ${input.requestedMarketId}, but its restaurant is in market ${input.expectedMarketId}`,
    );
    this.name = "MarketMismatchError";
    this.orderId = input.orderId;
    this.expectedMarketId = input.expectedMarketId;
    this.requestedMarketId = input.requestedMarketId;
  }
}
