/* eslint-disable */
/** Internal type. DO NOT USE DIRECTLY. */
type Exact<T extends { [key: string]: unknown }> = { [K in keyof T]: T[K] };
/** Internal type. DO NOT USE DIRECTLY. */
export type Incremental<T> = T | { [P in keyof T]?: P extends ' $fragmentName' | '__typename' ? T[P] : never };
import { TypedDocumentNode as DocumentNode } from '@graphql-typed-document-node/core';
/** Business reason why a Cook Pool command cannot be applied */
export type CookPoolProblemCode =
  /** The cook is preparing an order and cannot leave the line */
  | 'COOK_BUSY'
  /** The name is empty or longer than 64 characters */
  | 'COOK_NAME_INVALID'
  /** The cook is on line and cannot leave the pool */
  | 'COOK_ON_LINE'
  /** The cook left the pool and cannot return */
  | 'COOK_RETIRED';

/** Availability of a courier for new deliveries */
export type CourierAvailability =
  /** Courier receives delivery offers */
  | 'AVAILABLE'
  /** Courier is on a delivery */
  | 'BUSY'
  /** Courier does not receive delivery offers */
  | 'UNAVAILABLE';

/** Command of the courier over the current delivery */
export type CourierDeliveryAction =
  /** Confirm the order is handed to the customer */
  | 'CONFIRM_DELIVERY'
  /** Confirm the order is picked up at the restaurant */
  | 'CONFIRM_PICKUP';

/** Stage of a delivery assigned to the courier */
export type CourierDeliveryStage =
  /** Courier is going to the restaurant */
  | 'ASSIGNED'
  /** Courier has the order and is going to the customer */
  | 'PICKED_UP';

/** Business reason why a Courier Pool command cannot be applied */
export type CourierPoolProblemCode =
  /** The name is empty or longer than 64 characters */
  | 'COURIER_NAME_INVALID'
  /** The courier is on line and cannot leave the pool */
  | 'COURIER_ON_LINE'
  /** The courier status forbids the change: busy couriers stay on line, retired ones off line */
  | 'COURIER_STATUS_CONFLICT';

/** Business reason why a courier command cannot be applied */
export type CourierProblemCode =
  /** The delivery is not in a stage that allows this command */
  | 'DELIVERY_STAGE_CONFLICT'
  /** The offer expired or was taken */
  | 'OFFER_NOT_AVAILABLE';

/** Command of the kitchen over one restaurant order */
export type KitchenAction =
  /** Accept a RECEIVED order into the kitchen queue */
  | 'ACCEPT'
  /** Mark a PREPARING order assembled */
  | 'MARK_ASSEMBLED'
  /** Mark an ASSEMBLED order ready for pickup */
  | 'MARK_READY'
  /** Reject a RECEIVED order */
  | 'REJECT'
  /** Start preparing a QUEUED order */
  | 'START_PREPARATION';

export type KitchenActionInput = {
  /** Command to apply */
  action: KitchenAction;
  /** Restaurant order identifier (KitchenOrder.id) */
  kitchenOrderId: string | number;
  /** Restaurant of the order */
  restaurantId: string | number;
};

/** Business reason why a kitchen command cannot be applied */
export type KitchenActionProblemCode =
  /** No cook on line is free to start preparing; the order stays in the queue */
  | 'NO_COOK_AVAILABLE'
  /** The order is not in a stage that allows this command */
  | 'ORDER_STAGE_CONFLICT';

/** Stage of a restaurant order in the kitchen */
export type KitchenOrderStage =
  /** Order is assembled and waits to be marked ready */
  | 'ASSEMBLED'
  /** Kitchen is preparing the order */
  | 'PREPARING'
  /** Order is accepted and waits in the kitchen queue */
  | 'QUEUED'
  /** Order is ready for pickup */
  | 'READY'
  /** Order arrived and waits for the kitchen to accept or reject it */
  | 'RECEIVED';

/** Accepted result of a cancellation request */
export type OrderCancellationOutcome =
  /** Order was already cancelled */
  | 'ALREADY_CANCELLED'
  /** Request is accepted and the order will be cancelled asynchronously */
  | 'REQUESTED';

/** Business reason why cancellation cannot be requested */
export type OrderCancellationProblemCode =
  /** Cancellation window has closed */
  | 'CANCELLATION_WINDOW_EXPIRED'
  /** Cancellation window has not opened yet */
  | 'CANCELLATION_WINDOW_NOT_OPEN'
  /** Order is already delivered or failed */
  | 'ORDER_ALREADY_FINAL';

/** Why an order ended without delivery */
export type OrderEndReason =
  /** Customer cancelled the order */
  | 'CUSTOMER_CANCELLED'
  /** Delivery did not succeed */
  | 'DELIVERY_FAILED'
  /** Order was not fulfilled for another reason */
  | 'NOT_FULFILLED'
  /** Restaurant could not accept the order */
  | 'RESTAURANT_REJECTED';

export type OrderLineInput = {
  /** Menu item identifier */
  menuItemId: string | number;
  /** Number of units, at least 1 */
  quantity: number;
};

/** Notable moment in the progress of an order */
export type OrderMilestoneKind =
  /** Order is confirmed */
  | 'CONFIRMED'
  /** Courier is assigned to the order */
  | 'COURIER_ASSIGNED'
  /** Order is delivered */
  | 'DELIVERED'
  /** Order ended without delivery */
  | 'ENDED'
  /** Courier picked the order up */
  | 'PICKED_UP'
  /** Order is placed */
  | 'PLACED'
  /** Kitchen started preparation */
  | 'PREPARATION_STARTED'
  /** Order is ready for pickup */
  | 'READY_FOR_PICKUP';

/** Customer-facing lifecycle of an order */
export type OrderStatus =
  /** Order is cancelled before fulfillment */
  | 'CANCELLED'
  /** Order is confirmed and waits for the kitchen */
  | 'CONFIRMED'
  /** Order is delivered */
  | 'DELIVERED'
  /** Order could not be fulfilled */
  | 'FAILED'
  /** Courier picked the order up and is on the way */
  | 'PICKED_UP'
  /** Kitchen is preparing the order */
  | 'PREPARING'
  /** Order is ready and waits for the courier */
  | 'READY_FOR_PICKUP'
  /** Order is placed and waits for confirmation */
  | 'RECEIVED';

export type PlaceOrderInput = {
  /** Client-generated key of this checkout attempt, reused on manual retry */
  idempotencyKey: string;
  /** Ordered items */
  items: Array<OrderLineInput>;
  /** Restaurant to order from */
  restaurantId: string | number;
};

/** Business reason why an order cannot be placed */
export type PlaceOrderProblemCode =
  /** One or more items cannot be ordered now */
  | 'ORDER_ITEM_UNAVAILABLE'
  /** Order total is above the allowed maximum */
  | 'ORDER_TOTAL_ABOVE_MAXIMUM'
  /** Order total is below the restaurant minimum */
  | 'ORDER_TOTAL_BELOW_MINIMUM'
  /** Restaurant does not accept orders now */
  | 'RESTAURANT_NOT_ACCEPTING_ORDERS';

export type RequestOrderCancellationInput = {
  /** Order to cancel */
  orderId: string | number;
  /** Optional free-text reason given by the customer */
  reason?: string | null | undefined;
  /** Client-generated key of this cancellation attempt, reused on manual retry */
  requestId: string;
};

export type BrowseRestaurantsQueryVariables = Exact<{
  first: number;
  after?: string | null | undefined;
}>;


export type BrowseRestaurantsQuery = { restaurants: { edges: Array<{ cursor: string, node: { id: string, name: string, acceptingOrders: boolean, menu: { items: Array<{ id: string, name: string }> } | null } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type ChooseMarketQueryVariables = Exact<{ [key: string]: never; }>;


export type ChooseMarketQuery = { markets: Array<{ id: string, name: string, currency: string }> };

export type DemoSimulatorQueryVariables = Exact<{ [key: string]: never; }>;


export type DemoSimulatorQuery = { simulator: { enabled: boolean } };

export type SetSimulatorEnabledMutationVariables = Exact<{
  enabled: boolean;
}>;


export type SetSimulatorEnabledMutation = { setSimulatorEnabled: { enabled: boolean } };

export type CookPoolViewFragment = { size: number, onLine: number, busy: number, queued: number, cooks: Array<{ id: string, name: string, onLine: boolean, currentKitchenOrderId: string | null }> };

export type KitchenBoardViewFragment = { restaurantId: string, restaurant: { id: string, name: string, acceptingOrders: boolean }, orders: Array<{ id: string, orderId: string, stage: KitchenOrderStage, receivedAt: string, acceptedAt: string | null, expectedReadyAt: string | null, items: Array<{ name: string, quantity: number }>, cook: { id: string, name: string } | null }>, cookPool: { size: number, onLine: number, busy: number, queued: number, cooks: Array<{ id: string, name: string, onLine: boolean, currentKitchenOrderId: string | null }> } };

export type BoardKitchenQueryVariables = Exact<{
  restaurantId: string | number;
}>;


export type BoardKitchenQuery = { kitchen: { restaurantId: string, restaurant: { id: string, name: string, acceptingOrders: boolean }, orders: Array<{ id: string, orderId: string, stage: KitchenOrderStage, receivedAt: string, acceptedAt: string | null, expectedReadyAt: string | null, items: Array<{ name: string, quantity: number }>, cook: { id: string, name: string } | null }>, cookPool: { size: number, onLine: number, busy: number, queued: number, cooks: Array<{ id: string, name: string, onLine: boolean, currentKitchenOrderId: string | null }> } } | null };

export type BoardKitchenChangedSubscriptionVariables = Exact<{
  restaurantId: string | number;
}>;


export type BoardKitchenChangedSubscription = { kitchenChanged: { restaurantId: string, restaurant: { id: string, name: string, acceptingOrders: boolean }, orders: Array<{ id: string, orderId: string, stage: KitchenOrderStage, receivedAt: string, acceptedAt: string | null, expectedReadyAt: string | null, items: Array<{ name: string, quantity: number }>, cook: { id: string, name: string } | null }>, cookPool: { size: number, onLine: number, busy: number, queued: number, cooks: Array<{ id: string, name: string, onLine: boolean, currentKitchenOrderId: string | null }> } } };

export type BoardOwnedRestaurantsQueryVariables = Exact<{ [key: string]: never; }>;


export type BoardOwnedRestaurantsQuery = { myRestaurants: { edges: Array<{ node: { id: string, name: string, acceptingOrders: boolean } }> } };

export type ApplyKitchenActionMutationVariables = Exact<{
  input: KitchenActionInput;
}>;


export type ApplyKitchenActionMutation = { applyKitchenAction: { problem: KitchenActionProblemCode | null, order: { id: string, orderId: string, stage: KitchenOrderStage, receivedAt: string, items: Array<{ name: string, quantity: number }>, cook: { id: string, name: string } | null } | null } };

export type HireCookMutationVariables = Exact<{
  restaurantId: string | number;
  name: string;
}>;


export type HireCookMutation = { hireCook: { problem: CookPoolProblemCode | null, cookPool: { size: number, onLine: number, busy: number, queued: number, cooks: Array<{ id: string, name: string, onLine: boolean, currentKitchenOrderId: string | null }> } } };

export type SetCookOnLineMutationVariables = Exact<{
  restaurantId: string | number;
  cookId: string | number;
  onLine: boolean;
}>;


export type SetCookOnLineMutation = { setCookOnLine: { problem: CookPoolProblemCode | null, cookPool: { size: number, onLine: number, busy: number, queued: number, cooks: Array<{ id: string, name: string, onLine: boolean, currentKitchenOrderId: string | null }> } } };

export type RetireCookMutationVariables = Exact<{
  restaurantId: string | number;
  cookId: string | number;
}>;


export type RetireCookMutation = { retireCook: { problem: CookPoolProblemCode | null, cookPool: { size: number, onLine: number, busy: number, queued: number, cooks: Array<{ id: string, name: string, onLine: boolean, currentKitchenOrderId: string | null }> } } };

export type CourierPoolViewFragment = { id: string, size: number, onLine: number, busy: number, waiting: number, couriers: Array<{ id: string, name: string, availability: CourierAvailability, offeredOrderId: string | null, currentOrderId: string | null }> };

export type BoardCourierPoolQueryVariables = Exact<{ [key: string]: never; }>;


export type BoardCourierPoolQuery = { courierPool: { id: string, size: number, onLine: number, busy: number, waiting: number, couriers: Array<{ id: string, name: string, availability: CourierAvailability, offeredOrderId: string | null, currentOrderId: string | null }> } };

export type BoardCourierPoolChangedSubscriptionVariables = Exact<{
  marketId: string | number;
}>;


export type BoardCourierPoolChangedSubscription = { courierPoolChanged: { id: string, size: number, onLine: number, busy: number, waiting: number, couriers: Array<{ id: string, name: string, availability: CourierAvailability, offeredOrderId: string | null, currentOrderId: string | null }> } };

export type AddCourierMutationVariables = Exact<{
  name: string;
}>;


export type AddCourierMutation = { addCourier: { problem: CourierPoolProblemCode | null, courierPool: { id: string, size: number, onLine: number, busy: number, waiting: number, couriers: Array<{ id: string, name: string, availability: CourierAvailability, offeredOrderId: string | null, currentOrderId: string | null }> } } };

export type SetCourierOnLineMutationVariables = Exact<{
  courierId: string | number;
  onLine: boolean;
}>;


export type SetCourierOnLineMutation = { setCourierOnLine: { problem: CourierPoolProblemCode | null, courierPool: { id: string, size: number, onLine: number, busy: number, waiting: number, couriers: Array<{ id: string, name: string, availability: CourierAvailability, offeredOrderId: string | null, currentOrderId: string | null }> } } };

export type RetireCourierMutationVariables = Exact<{
  courierId: string | number;
}>;


export type RetireCourierMutation = { retireCourier: { problem: CourierPoolProblemCode | null, courierPool: { id: string, size: number, onLine: number, busy: number, waiting: number, couriers: Array<{ id: string, name: string, availability: CourierAvailability, offeredOrderId: string | null, currentOrderId: string | null }> } } };

export type CourierWorkFragment = { id: string, availability: CourierAvailability, offers: Array<{ id: string, deliveryId: string, orderId: string, expiresAt: string }>, currentDelivery: { id: string, orderId: string, stage: CourierDeliveryStage } | null };

export type BoardCourierQueryVariables = Exact<{ [key: string]: never; }>;


export type BoardCourierQuery = { courier: { id: string, availability: CourierAvailability, offers: Array<{ id: string, deliveryId: string, orderId: string, expiresAt: string }>, currentDelivery: { id: string, orderId: string, stage: CourierDeliveryStage } | null } | null };

export type BoardCourierChangedSubscriptionVariables = Exact<{
  id: string | number;
}>;


export type BoardCourierChangedSubscription = { courierChanged: { id: string, availability: CourierAvailability, offers: Array<{ id: string, deliveryId: string, orderId: string, expiresAt: string }>, currentDelivery: { id: string, orderId: string, stage: CourierDeliveryStage } | null } };

export type SetCourierAvailabilityMutationVariables = Exact<{
  availability: CourierAvailability;
}>;


export type SetCourierAvailabilityMutation = { setCourierAvailability: { problem: CourierProblemCode | null, courier: { id: string, availability: CourierAvailability, offers: Array<{ id: string, deliveryId: string, orderId: string, expiresAt: string }>, currentDelivery: { id: string, orderId: string, stage: CourierDeliveryStage } | null } } };

export type AcceptDeliveryOfferMutationVariables = Exact<{
  offerId: string | number;
}>;


export type AcceptDeliveryOfferMutation = { acceptDeliveryOffer: { problem: CourierProblemCode | null, courier: { id: string, availability: CourierAvailability, offers: Array<{ id: string, deliveryId: string, orderId: string, expiresAt: string }>, currentDelivery: { id: string, orderId: string, stage: CourierDeliveryStage } | null } } };

export type ApplyCourierDeliveryActionMutationVariables = Exact<{
  deliveryId: string | number;
  action: CourierDeliveryAction;
}>;


export type ApplyCourierDeliveryActionMutation = { applyCourierDeliveryAction: { problem: CourierProblemCode | null, courier: { id: string, availability: CourierAvailability, offers: Array<{ id: string, deliveryId: string, orderId: string, expiresAt: string }>, currentDelivery: { id: string, orderId: string, stage: CourierDeliveryStage } | null } } };

export type BoardOrderViewFragment = { id: string, status: OrderStatus, endReason: OrderEndReason | null, placedAt: string, restaurant: { id: string, name: string }, total: { amount: string, currency: string } };

export type BoardOrderStatusQueryVariables = Exact<{
  id: string | number;
}>;


export type BoardOrderStatusQuery = { order: { id: string, status: OrderStatus, endReason: OrderEndReason | null, placedAt: string, restaurant: { id: string, name: string }, total: { amount: string, currency: string } } | null };

export type BoardOrderChangedSubscriptionVariables = Exact<{
  id: string | number;
}>;


export type BoardOrderChangedSubscription = { orderChanged: { id: string, status: OrderStatus, endReason: OrderEndReason | null, placedAt: string, restaurant: { id: string, name: string }, total: { amount: string, currency: string } } };

export type OpenRestaurantMutationVariables = Exact<{
  restaurantId: string | number;
}>;


export type OpenRestaurantMutation = { openRestaurant: { changed: boolean, restaurant: { id: string, name: string, acceptingOrders: boolean } } };

export type CloseRestaurantMutationVariables = Exact<{
  restaurantId: string | number;
}>;


export type CloseRestaurantMutation = { closeRestaurant: { changed: boolean, restaurant: { id: string, name: string, acceptingOrders: boolean } } };

export type PlaceOrderMutationVariables = Exact<{
  input: PlaceOrderInput;
}>;


export type PlaceOrderMutation = { placeOrder: { order: { id: string } | null, problems: Array<{ code: PlaceOrderProblemCode, menuItemIds: Array<string> }> } };

export type BffInfoQueryVariables = Exact<{ [key: string]: never; }>;


export type BffInfoQuery = { bffInfo: { name: string, version: string } };

export type ViewedOrderFragment = { id: string, status: OrderStatus, endReason: OrderEndReason | null, placedAt: string, expectedReadyAt: string | null, expectedPickupAt: string | null, expectedDeliveredAt: string | null, restaurant: { id: string, name: string }, items: Array<{ quantity: number, menuItem: { id: string, name: string }, unitPrice: { amount: string, currency: string }, lineTotal: { amount: string, currency: string } }>, total: { amount: string, currency: string }, milestones: Array<{ kind: OrderMilestoneKind, occurredAt: string }>, cancellation: { availableUntil: string } | null, courier: { name: string } | null };

export type ViewOrderQueryVariables = Exact<{
  id: string | number;
}>;


export type ViewOrderQuery = { order: { id: string, status: OrderStatus, endReason: OrderEndReason | null, placedAt: string, expectedReadyAt: string | null, expectedPickupAt: string | null, expectedDeliveredAt: string | null, restaurant: { id: string, name: string }, items: Array<{ quantity: number, menuItem: { id: string, name: string }, unitPrice: { amount: string, currency: string }, lineTotal: { amount: string, currency: string } }>, total: { amount: string, currency: string }, milestones: Array<{ kind: OrderMilestoneKind, occurredAt: string }>, cancellation: { availableUntil: string } | null, courier: { name: string } | null } | null };

export type OrderChangedSubscriptionVariables = Exact<{
  id: string | number;
}>;


export type OrderChangedSubscription = { orderChanged: { id: string, status: OrderStatus, endReason: OrderEndReason | null, placedAt: string, expectedReadyAt: string | null, expectedPickupAt: string | null, expectedDeliveredAt: string | null, restaurant: { id: string, name: string }, items: Array<{ quantity: number, menuItem: { id: string, name: string }, unitPrice: { amount: string, currency: string }, lineTotal: { amount: string, currency: string } }>, total: { amount: string, currency: string }, milestones: Array<{ kind: OrderMilestoneKind, occurredAt: string }>, cancellation: { availableUntil: string } | null, courier: { name: string } | null } };

export type RequestOrderCancellationMutationVariables = Exact<{
  input: RequestOrderCancellationInput;
}>;


export type RequestOrderCancellationMutation = { requestOrderCancellation: { outcome: OrderCancellationOutcome | null, problem: { code: OrderCancellationProblemCode } | null } };

export type ViewRestaurantQueryVariables = Exact<{
  id: string | number;
}>;


export type ViewRestaurantQuery = { restaurant: { id: string, name: string, acceptingOrders: boolean, menu: { items: Array<{ id: string, name: string, description: string | null, available: boolean, price: { amount: string, currency: string } }> } | null } | null };

export const CookPoolViewFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CookPoolView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"CookPool"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"busy"}},{"kind":"Field","name":{"kind":"Name","value":"queued"}},{"kind":"Field","name":{"kind":"Name","value":"cooks"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"currentKitchenOrderId"}}]}}]}}]} as unknown as DocumentNode<CookPoolViewFragment, unknown>;
export const KitchenBoardViewFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"KitchenBoardView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Kitchen"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restaurantId"}},{"kind":"Field","name":{"kind":"Name","value":"restaurant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"acceptingOrders"}}]}},{"kind":"Field","name":{"kind":"Name","value":"orders"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"stage"}},{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"quantity"}}]}},{"kind":"Field","name":{"kind":"Name","value":"receivedAt"}},{"kind":"Field","name":{"kind":"Name","value":"acceptedAt"}},{"kind":"Field","name":{"kind":"Name","value":"expectedReadyAt"}},{"kind":"Field","name":{"kind":"Name","value":"cook"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"cookPool"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CookPoolView"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CookPoolView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"CookPool"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"busy"}},{"kind":"Field","name":{"kind":"Name","value":"queued"}},{"kind":"Field","name":{"kind":"Name","value":"cooks"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"currentKitchenOrderId"}}]}}]}}]} as unknown as DocumentNode<KitchenBoardViewFragment, unknown>;
export const CourierPoolViewFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CourierPoolView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"CourierPool"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"busy"}},{"kind":"Field","name":{"kind":"Name","value":"waiting"}},{"kind":"Field","name":{"kind":"Name","value":"couriers"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"availability"}},{"kind":"Field","name":{"kind":"Name","value":"offeredOrderId"}},{"kind":"Field","name":{"kind":"Name","value":"currentOrderId"}}]}}]}}]} as unknown as DocumentNode<CourierPoolViewFragment, unknown>;
export const CourierWorkFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CourierWork"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Courier"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"availability"}},{"kind":"Field","name":{"kind":"Name","value":"offers"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"deliveryId"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"currentDelivery"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"stage"}}]}}]}}]} as unknown as DocumentNode<CourierWorkFragment, unknown>;
export const BoardOrderViewFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"BoardOrderView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Order"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"endReason"}},{"kind":"Field","name":{"kind":"Name","value":"restaurant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"total"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}},{"kind":"Field","name":{"kind":"Name","value":"placedAt"}}]}}]} as unknown as DocumentNode<BoardOrderViewFragment, unknown>;
export const ViewedOrderFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"ViewedOrder"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Order"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"endReason"}},{"kind":"Field","name":{"kind":"Name","value":"restaurant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"menuItem"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"quantity"}},{"kind":"Field","name":{"kind":"Name","value":"unitPrice"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}},{"kind":"Field","name":{"kind":"Name","value":"lineTotal"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"total"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}},{"kind":"Field","name":{"kind":"Name","value":"placedAt"}},{"kind":"Field","name":{"kind":"Name","value":"milestones"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"occurredAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"cancellation"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"availableUntil"}}]}},{"kind":"Field","name":{"kind":"Name","value":"expectedReadyAt"}},{"kind":"Field","name":{"kind":"Name","value":"expectedPickupAt"}},{"kind":"Field","name":{"kind":"Name","value":"expectedDeliveredAt"}},{"kind":"Field","name":{"kind":"Name","value":"courier"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]} as unknown as DocumentNode<ViewedOrderFragment, unknown>;
export const BrowseRestaurantsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"BrowseRestaurants"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restaurants"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cursor"}},{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"acceptingOrders"}},{"kind":"Field","name":{"kind":"Name","value":"menu"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}}]} as unknown as DocumentNode<BrowseRestaurantsQuery, BrowseRestaurantsQueryVariables>;
export const ChooseMarketDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ChooseMarket"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"markets"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}}]}}]} as unknown as DocumentNode<ChooseMarketQuery, ChooseMarketQueryVariables>;
export const DemoSimulatorDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"DemoSimulator"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"simulator"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}}]}}]}}]} as unknown as DocumentNode<DemoSimulatorQuery, DemoSimulatorQueryVariables>;
export const SetSimulatorEnabledDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"SetSimulatorEnabled"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"enabled"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Boolean"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"setSimulatorEnabled"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"enabled"},"value":{"kind":"Variable","name":{"kind":"Name","value":"enabled"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}}]}}]}}]} as unknown as DocumentNode<SetSimulatorEnabledMutation, SetSimulatorEnabledMutationVariables>;
export const BoardKitchenDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"BoardKitchen"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"restaurantId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kitchen"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"restaurantId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"restaurantId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"KitchenBoardView"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CookPoolView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"CookPool"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"busy"}},{"kind":"Field","name":{"kind":"Name","value":"queued"}},{"kind":"Field","name":{"kind":"Name","value":"cooks"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"currentKitchenOrderId"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"KitchenBoardView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Kitchen"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restaurantId"}},{"kind":"Field","name":{"kind":"Name","value":"restaurant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"acceptingOrders"}}]}},{"kind":"Field","name":{"kind":"Name","value":"orders"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"stage"}},{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"quantity"}}]}},{"kind":"Field","name":{"kind":"Name","value":"receivedAt"}},{"kind":"Field","name":{"kind":"Name","value":"acceptedAt"}},{"kind":"Field","name":{"kind":"Name","value":"expectedReadyAt"}},{"kind":"Field","name":{"kind":"Name","value":"cook"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"cookPool"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CookPoolView"}}]}}]}}]} as unknown as DocumentNode<BoardKitchenQuery, BoardKitchenQueryVariables>;
export const BoardKitchenChangedDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"BoardKitchenChanged"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"restaurantId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kitchenChanged"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"restaurantId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"restaurantId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"KitchenBoardView"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CookPoolView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"CookPool"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"busy"}},{"kind":"Field","name":{"kind":"Name","value":"queued"}},{"kind":"Field","name":{"kind":"Name","value":"cooks"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"currentKitchenOrderId"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"KitchenBoardView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Kitchen"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restaurantId"}},{"kind":"Field","name":{"kind":"Name","value":"restaurant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"acceptingOrders"}}]}},{"kind":"Field","name":{"kind":"Name","value":"orders"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"stage"}},{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"quantity"}}]}},{"kind":"Field","name":{"kind":"Name","value":"receivedAt"}},{"kind":"Field","name":{"kind":"Name","value":"acceptedAt"}},{"kind":"Field","name":{"kind":"Name","value":"expectedReadyAt"}},{"kind":"Field","name":{"kind":"Name","value":"cook"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"cookPool"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CookPoolView"}}]}}]}}]} as unknown as DocumentNode<BoardKitchenChangedSubscription, BoardKitchenChangedSubscriptionVariables>;
export const BoardOwnedRestaurantsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"BoardOwnedRestaurants"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"myRestaurants"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"20"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"acceptingOrders"}}]}}]}}]}}]}}]} as unknown as DocumentNode<BoardOwnedRestaurantsQuery, BoardOwnedRestaurantsQueryVariables>;
export const ApplyKitchenActionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ApplyKitchenAction"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"KitchenActionInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"applyKitchenAction"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"order"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"stage"}},{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"quantity"}}]}},{"kind":"Field","name":{"kind":"Name","value":"receivedAt"}},{"kind":"Field","name":{"kind":"Name","value":"cook"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"problem"}}]}}]}}]} as unknown as DocumentNode<ApplyKitchenActionMutation, ApplyKitchenActionMutationVariables>;
export const HireCookDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"HireCook"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"restaurantId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"name"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hireCook"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"restaurantId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"restaurantId"}}},{"kind":"Argument","name":{"kind":"Name","value":"name"},"value":{"kind":"Variable","name":{"kind":"Name","value":"name"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cookPool"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CookPoolView"}}]}},{"kind":"Field","name":{"kind":"Name","value":"problem"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CookPoolView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"CookPool"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"busy"}},{"kind":"Field","name":{"kind":"Name","value":"queued"}},{"kind":"Field","name":{"kind":"Name","value":"cooks"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"currentKitchenOrderId"}}]}}]}}]} as unknown as DocumentNode<HireCookMutation, HireCookMutationVariables>;
export const SetCookOnLineDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"SetCookOnLine"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"restaurantId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"cookId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"onLine"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Boolean"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"setCookOnLine"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"restaurantId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"restaurantId"}}},{"kind":"Argument","name":{"kind":"Name","value":"cookId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"cookId"}}},{"kind":"Argument","name":{"kind":"Name","value":"onLine"},"value":{"kind":"Variable","name":{"kind":"Name","value":"onLine"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cookPool"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CookPoolView"}}]}},{"kind":"Field","name":{"kind":"Name","value":"problem"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CookPoolView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"CookPool"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"busy"}},{"kind":"Field","name":{"kind":"Name","value":"queued"}},{"kind":"Field","name":{"kind":"Name","value":"cooks"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"currentKitchenOrderId"}}]}}]}}]} as unknown as DocumentNode<SetCookOnLineMutation, SetCookOnLineMutationVariables>;
export const RetireCookDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RetireCook"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"restaurantId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"cookId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"retireCook"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"restaurantId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"restaurantId"}}},{"kind":"Argument","name":{"kind":"Name","value":"cookId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"cookId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cookPool"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CookPoolView"}}]}},{"kind":"Field","name":{"kind":"Name","value":"problem"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CookPoolView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"CookPool"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"busy"}},{"kind":"Field","name":{"kind":"Name","value":"queued"}},{"kind":"Field","name":{"kind":"Name","value":"cooks"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"currentKitchenOrderId"}}]}}]}}]} as unknown as DocumentNode<RetireCookMutation, RetireCookMutationVariables>;
export const BoardCourierPoolDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"BoardCourierPool"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"courierPool"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CourierPoolView"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CourierPoolView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"CourierPool"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"busy"}},{"kind":"Field","name":{"kind":"Name","value":"waiting"}},{"kind":"Field","name":{"kind":"Name","value":"couriers"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"availability"}},{"kind":"Field","name":{"kind":"Name","value":"offeredOrderId"}},{"kind":"Field","name":{"kind":"Name","value":"currentOrderId"}}]}}]}}]} as unknown as DocumentNode<BoardCourierPoolQuery, BoardCourierPoolQueryVariables>;
export const BoardCourierPoolChangedDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"BoardCourierPoolChanged"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"marketId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"courierPoolChanged"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"marketId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"marketId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CourierPoolView"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CourierPoolView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"CourierPool"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"busy"}},{"kind":"Field","name":{"kind":"Name","value":"waiting"}},{"kind":"Field","name":{"kind":"Name","value":"couriers"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"availability"}},{"kind":"Field","name":{"kind":"Name","value":"offeredOrderId"}},{"kind":"Field","name":{"kind":"Name","value":"currentOrderId"}}]}}]}}]} as unknown as DocumentNode<BoardCourierPoolChangedSubscription, BoardCourierPoolChangedSubscriptionVariables>;
export const AddCourierDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AddCourier"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"name"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"addCourier"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"name"},"value":{"kind":"Variable","name":{"kind":"Name","value":"name"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"courierPool"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CourierPoolView"}}]}},{"kind":"Field","name":{"kind":"Name","value":"problem"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CourierPoolView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"CourierPool"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"busy"}},{"kind":"Field","name":{"kind":"Name","value":"waiting"}},{"kind":"Field","name":{"kind":"Name","value":"couriers"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"availability"}},{"kind":"Field","name":{"kind":"Name","value":"offeredOrderId"}},{"kind":"Field","name":{"kind":"Name","value":"currentOrderId"}}]}}]}}]} as unknown as DocumentNode<AddCourierMutation, AddCourierMutationVariables>;
export const SetCourierOnLineDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"SetCourierOnLine"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"courierId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"onLine"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Boolean"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"setCourierOnLine"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"courierId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"courierId"}}},{"kind":"Argument","name":{"kind":"Name","value":"onLine"},"value":{"kind":"Variable","name":{"kind":"Name","value":"onLine"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"courierPool"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CourierPoolView"}}]}},{"kind":"Field","name":{"kind":"Name","value":"problem"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CourierPoolView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"CourierPool"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"busy"}},{"kind":"Field","name":{"kind":"Name","value":"waiting"}},{"kind":"Field","name":{"kind":"Name","value":"couriers"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"availability"}},{"kind":"Field","name":{"kind":"Name","value":"offeredOrderId"}},{"kind":"Field","name":{"kind":"Name","value":"currentOrderId"}}]}}]}}]} as unknown as DocumentNode<SetCourierOnLineMutation, SetCourierOnLineMutationVariables>;
export const RetireCourierDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RetireCourier"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"courierId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"retireCourier"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"courierId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"courierId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"courierPool"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CourierPoolView"}}]}},{"kind":"Field","name":{"kind":"Name","value":"problem"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CourierPoolView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"CourierPool"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"onLine"}},{"kind":"Field","name":{"kind":"Name","value":"busy"}},{"kind":"Field","name":{"kind":"Name","value":"waiting"}},{"kind":"Field","name":{"kind":"Name","value":"couriers"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"availability"}},{"kind":"Field","name":{"kind":"Name","value":"offeredOrderId"}},{"kind":"Field","name":{"kind":"Name","value":"currentOrderId"}}]}}]}}]} as unknown as DocumentNode<RetireCourierMutation, RetireCourierMutationVariables>;
export const BoardCourierDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"BoardCourier"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"courier"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CourierWork"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CourierWork"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Courier"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"availability"}},{"kind":"Field","name":{"kind":"Name","value":"offers"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"deliveryId"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"currentDelivery"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"stage"}}]}}]}}]} as unknown as DocumentNode<BoardCourierQuery, BoardCourierQueryVariables>;
export const BoardCourierChangedDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"BoardCourierChanged"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"courierChanged"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CourierWork"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CourierWork"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Courier"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"availability"}},{"kind":"Field","name":{"kind":"Name","value":"offers"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"deliveryId"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"currentDelivery"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"stage"}}]}}]}}]} as unknown as DocumentNode<BoardCourierChangedSubscription, BoardCourierChangedSubscriptionVariables>;
export const SetCourierAvailabilityDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"SetCourierAvailability"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"availability"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CourierAvailability"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"setCourierAvailability"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"availability"},"value":{"kind":"Variable","name":{"kind":"Name","value":"availability"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"courier"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CourierWork"}}]}},{"kind":"Field","name":{"kind":"Name","value":"problem"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CourierWork"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Courier"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"availability"}},{"kind":"Field","name":{"kind":"Name","value":"offers"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"deliveryId"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"currentDelivery"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"stage"}}]}}]}}]} as unknown as DocumentNode<SetCourierAvailabilityMutation, SetCourierAvailabilityMutationVariables>;
export const AcceptDeliveryOfferDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AcceptDeliveryOffer"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"offerId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"acceptDeliveryOffer"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"offerId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"offerId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"courier"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CourierWork"}}]}},{"kind":"Field","name":{"kind":"Name","value":"problem"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CourierWork"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Courier"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"availability"}},{"kind":"Field","name":{"kind":"Name","value":"offers"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"deliveryId"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"currentDelivery"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"stage"}}]}}]}}]} as unknown as DocumentNode<AcceptDeliveryOfferMutation, AcceptDeliveryOfferMutationVariables>;
export const ApplyCourierDeliveryActionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ApplyCourierDeliveryAction"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"deliveryId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"action"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CourierDeliveryAction"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"applyCourierDeliveryAction"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"deliveryId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"deliveryId"}}},{"kind":"Argument","name":{"kind":"Name","value":"action"},"value":{"kind":"Variable","name":{"kind":"Name","value":"action"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"courier"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"CourierWork"}}]}},{"kind":"Field","name":{"kind":"Name","value":"problem"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"CourierWork"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Courier"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"availability"}},{"kind":"Field","name":{"kind":"Name","value":"offers"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"deliveryId"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"currentDelivery"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"orderId"}},{"kind":"Field","name":{"kind":"Name","value":"stage"}}]}}]}}]} as unknown as DocumentNode<ApplyCourierDeliveryActionMutation, ApplyCourierDeliveryActionMutationVariables>;
export const BoardOrderStatusDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"BoardOrderStatus"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"order"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"BoardOrderView"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"BoardOrderView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Order"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"endReason"}},{"kind":"Field","name":{"kind":"Name","value":"restaurant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"total"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}},{"kind":"Field","name":{"kind":"Name","value":"placedAt"}}]}}]} as unknown as DocumentNode<BoardOrderStatusQuery, BoardOrderStatusQueryVariables>;
export const BoardOrderChangedDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"BoardOrderChanged"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"orderChanged"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"BoardOrderView"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"BoardOrderView"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Order"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"endReason"}},{"kind":"Field","name":{"kind":"Name","value":"restaurant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"total"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}},{"kind":"Field","name":{"kind":"Name","value":"placedAt"}}]}}]} as unknown as DocumentNode<BoardOrderChangedSubscription, BoardOrderChangedSubscriptionVariables>;
export const OpenRestaurantDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"OpenRestaurant"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"restaurantId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"openRestaurant"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"restaurantId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"restaurantId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"changed"}},{"kind":"Field","name":{"kind":"Name","value":"restaurant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"acceptingOrders"}}]}}]}}]}}]} as unknown as DocumentNode<OpenRestaurantMutation, OpenRestaurantMutationVariables>;
export const CloseRestaurantDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CloseRestaurant"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"restaurantId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"closeRestaurant"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"restaurantId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"restaurantId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"changed"}},{"kind":"Field","name":{"kind":"Name","value":"restaurant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"acceptingOrders"}}]}}]}}]}}]} as unknown as DocumentNode<CloseRestaurantMutation, CloseRestaurantMutationVariables>;
export const PlaceOrderDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"PlaceOrder"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"PlaceOrderInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"placeOrder"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"order"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}}]}},{"kind":"Field","name":{"kind":"Name","value":"problems"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"menuItemIds"}}]}}]}}]}}]} as unknown as DocumentNode<PlaceOrderMutation, PlaceOrderMutationVariables>;
export const BffInfoDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"BffInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"bffInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}}]}}]} as unknown as DocumentNode<BffInfoQuery, BffInfoQueryVariables>;
export const ViewOrderDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ViewOrder"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"order"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"ViewedOrder"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"ViewedOrder"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Order"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"endReason"}},{"kind":"Field","name":{"kind":"Name","value":"restaurant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"menuItem"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"quantity"}},{"kind":"Field","name":{"kind":"Name","value":"unitPrice"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}},{"kind":"Field","name":{"kind":"Name","value":"lineTotal"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"total"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}},{"kind":"Field","name":{"kind":"Name","value":"placedAt"}},{"kind":"Field","name":{"kind":"Name","value":"milestones"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"occurredAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"cancellation"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"availableUntil"}}]}},{"kind":"Field","name":{"kind":"Name","value":"expectedReadyAt"}},{"kind":"Field","name":{"kind":"Name","value":"expectedPickupAt"}},{"kind":"Field","name":{"kind":"Name","value":"expectedDeliveredAt"}},{"kind":"Field","name":{"kind":"Name","value":"courier"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]} as unknown as DocumentNode<ViewOrderQuery, ViewOrderQueryVariables>;
export const OrderChangedDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"OrderChanged"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"orderChanged"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"ViewedOrder"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"ViewedOrder"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Order"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"endReason"}},{"kind":"Field","name":{"kind":"Name","value":"restaurant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"menuItem"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"quantity"}},{"kind":"Field","name":{"kind":"Name","value":"unitPrice"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}},{"kind":"Field","name":{"kind":"Name","value":"lineTotal"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"total"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}},{"kind":"Field","name":{"kind":"Name","value":"placedAt"}},{"kind":"Field","name":{"kind":"Name","value":"milestones"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"occurredAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"cancellation"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"availableUntil"}}]}},{"kind":"Field","name":{"kind":"Name","value":"expectedReadyAt"}},{"kind":"Field","name":{"kind":"Name","value":"expectedPickupAt"}},{"kind":"Field","name":{"kind":"Name","value":"expectedDeliveredAt"}},{"kind":"Field","name":{"kind":"Name","value":"courier"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]} as unknown as DocumentNode<OrderChangedSubscription, OrderChangedSubscriptionVariables>;
export const RequestOrderCancellationDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RequestOrderCancellation"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"RequestOrderCancellationInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"requestOrderCancellation"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"outcome"}},{"kind":"Field","name":{"kind":"Name","value":"problem"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}}]}}]}}]}}]} as unknown as DocumentNode<RequestOrderCancellationMutation, RequestOrderCancellationMutationVariables>;
export const ViewRestaurantDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ViewRestaurant"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restaurant"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"acceptingOrders"}},{"kind":"Field","name":{"kind":"Name","value":"menu"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"description"}},{"kind":"Field","name":{"kind":"Name","value":"price"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}},{"kind":"Field","name":{"kind":"Name","value":"available"}}]}}]}}]}}]}}]} as unknown as DocumentNode<ViewRestaurantQuery, ViewRestaurantQueryVariables>;