/* eslint-disable */
/** Internal type. DO NOT USE DIRECTLY. */
type Exact<T extends { [key: string]: unknown }> = { [K in keyof T]: T[K] };
/** Internal type. DO NOT USE DIRECTLY. */
export type Incremental<T> = T | { [P in keyof T]?: P extends ' $fragmentName' | '__typename' ? T[P] : never };
import { TypedDocumentNode as DocumentNode } from '@graphql-typed-document-node/core';
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
  /** Delivery address */
  deliveryAddress: string;
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


export type BrowseRestaurantsQuery = { restaurants: { edges: Array<{ cursor: string, node: { id: string, name: string, address: string, acceptingOrders: boolean } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type PlaceOrderMutationVariables = Exact<{
  input: PlaceOrderInput;
}>;


export type PlaceOrderMutation = { placeOrder: { order: { id: string } | null, problems: Array<{ code: PlaceOrderProblemCode, menuItemIds: Array<string> }> } };

export type BffInfoQueryVariables = Exact<{ [key: string]: never; }>;


export type BffInfoQuery = { bffInfo: { name: string, version: string } };

export type ViewOrderQueryVariables = Exact<{
  id: string | number;
}>;


export type ViewOrderQuery = { order: { id: string, status: OrderStatus, endReason: OrderEndReason | null, deliveryAddress: string, placedAt: string, restaurant: { id: string, name: string }, items: Array<{ quantity: number, menuItem: { id: string, name: string }, unitPrice: { amount: string, currency: string }, lineTotal: { amount: string, currency: string } }>, total: { amount: string, currency: string }, milestones: Array<{ kind: OrderMilestoneKind, occurredAt: string }>, cancellation: { availableUntil: string } | null } | null };

export type RequestOrderCancellationMutationVariables = Exact<{
  input: RequestOrderCancellationInput;
}>;


export type RequestOrderCancellationMutation = { requestOrderCancellation: { outcome: OrderCancellationOutcome | null, problem: { code: OrderCancellationProblemCode } | null } };

export type ViewRestaurantQueryVariables = Exact<{
  id: string | number;
}>;


export type ViewRestaurantQuery = { restaurant: { id: string, name: string, address: string, acceptingOrders: boolean, menu: { items: Array<{ id: string, name: string, description: string | null, available: boolean, price: { amount: string, currency: string } }> } | null } | null };


export const BrowseRestaurantsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"BrowseRestaurants"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restaurants"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cursor"}},{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"address"}},{"kind":"Field","name":{"kind":"Name","value":"acceptingOrders"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}}]} as unknown as DocumentNode<BrowseRestaurantsQuery, BrowseRestaurantsQueryVariables>;
export const PlaceOrderDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"PlaceOrder"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"PlaceOrderInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"placeOrder"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"order"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}}]}},{"kind":"Field","name":{"kind":"Name","value":"problems"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"menuItemIds"}}]}}]}}]}}]} as unknown as DocumentNode<PlaceOrderMutation, PlaceOrderMutationVariables>;
export const BffInfoDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"BffInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"bffInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"version"}}]}}]}}]} as unknown as DocumentNode<BffInfoQuery, BffInfoQueryVariables>;
export const ViewOrderDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ViewOrder"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"order"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"endReason"}},{"kind":"Field","name":{"kind":"Name","value":"restaurant"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"menuItem"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"quantity"}},{"kind":"Field","name":{"kind":"Name","value":"unitPrice"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}},{"kind":"Field","name":{"kind":"Name","value":"lineTotal"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"total"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}},{"kind":"Field","name":{"kind":"Name","value":"deliveryAddress"}},{"kind":"Field","name":{"kind":"Name","value":"placedAt"}},{"kind":"Field","name":{"kind":"Name","value":"milestones"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"occurredAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"cancellation"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"availableUntil"}}]}}]}}]}}]} as unknown as DocumentNode<ViewOrderQuery, ViewOrderQueryVariables>;
export const RequestOrderCancellationDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RequestOrderCancellation"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"RequestOrderCancellationInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"requestOrderCancellation"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"outcome"}},{"kind":"Field","name":{"kind":"Name","value":"problem"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}}]}}]}}]}}]} as unknown as DocumentNode<RequestOrderCancellationMutation, RequestOrderCancellationMutationVariables>;
export const ViewRestaurantDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ViewRestaurant"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restaurant"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"address"}},{"kind":"Field","name":{"kind":"Name","value":"acceptingOrders"}},{"kind":"Field","name":{"kind":"Name","value":"menu"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"description"}},{"kind":"Field","name":{"kind":"Name","value":"price"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"amount"}},{"kind":"Field","name":{"kind":"Name","value":"currency"}}]}},{"kind":"Field","name":{"kind":"Name","value":"available"}}]}}]}}]}}]}}]} as unknown as DocumentNode<ViewRestaurantQuery, ViewRestaurantQueryVariables>;