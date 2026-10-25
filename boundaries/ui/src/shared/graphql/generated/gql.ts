/* eslint-disable */
import * as types from './graphql';
import { TypedDocumentNode as DocumentNode } from '@graphql-typed-document-node/core';

/**
 * Map of all GraphQL operations in the project.
 *
 * This map has several performance disadvantages:
 * 1. It is not tree-shakeable, so it will include all operations in the project.
 * 2. It is not minifiable, so the string of a GraphQL query will be multiple times inside the bundle.
 * 3. It does not support dead code elimination, so it will add unused operations.
 *
 * Therefore it is highly recommended to use the babel or swc plugin for production.
 * Learn more about it here: https://the-guild.dev/graphql/codegen/plugins/presets/preset-client#reducing-bundle-size
 */
type Documents = {
    "query BrowseRestaurants($first: Int!, $after: String) {\n  restaurants(first: $first, after: $after) {\n    edges {\n      cursor\n      node {\n        id\n        name\n        address\n        acceptingOrders\n      }\n    }\n    pageInfo {\n      hasNextPage\n      endCursor\n    }\n  }\n}": typeof types.BrowseRestaurantsDocument,
    "query ManageRestaurant($id: ID!) {\n  restaurant(id: $id) {\n    id\n    name\n    address\n    acceptingOrders\n  }\n}\n\nmutation OpenRestaurant($restaurantId: ID!) {\n  openRestaurant(restaurantId: $restaurantId) {\n    changed\n    restaurant {\n      id\n      name\n      address\n      acceptingOrders\n    }\n  }\n}\n\nmutation CloseRestaurant($restaurantId: ID!) {\n  closeRestaurant(restaurantId: $restaurantId) {\n    changed\n    restaurant {\n      id\n      name\n      address\n      acceptingOrders\n    }\n  }\n}\n\nquery MyRestaurants($first: Int!) {\n  myRestaurants(first: $first) {\n    edges {\n      node {\n        id\n        name\n        address\n        acceptingOrders\n      }\n    }\n  }\n}": typeof types.ManageRestaurantDocument,
    "mutation PlaceOrder($input: PlaceOrderInput!) {\n  placeOrder(input: $input) {\n    order {\n      id\n    }\n    problems {\n      code\n      menuItemIds\n    }\n  }\n}": typeof types.PlaceOrderDocument,
    "query BffInfo {\n  bffInfo {\n    name\n    version\n  }\n}": typeof types.BffInfoDocument,
    "query ViewOrder($id: ID!) {\n  order(id: $id) {\n    id\n    status\n    endReason\n    restaurant {\n      id\n      name\n    }\n    items {\n      menuItem {\n        id\n        name\n      }\n      quantity\n      unitPrice {\n        amount\n        currency\n      }\n      lineTotal {\n        amount\n        currency\n      }\n    }\n    total {\n      amount\n      currency\n    }\n    deliveryAddress\n    placedAt\n    milestones {\n      kind\n      occurredAt\n    }\n    cancellation {\n      availableUntil\n    }\n  }\n}\n\nmutation RequestOrderCancellation($input: RequestOrderCancellationInput!) {\n  requestOrderCancellation(input: $input) {\n    outcome\n    problem {\n      code\n    }\n  }\n}": typeof types.ViewOrderDocument,
    "query ViewRestaurant($id: ID!) {\n  restaurant(id: $id) {\n    id\n    name\n    address\n    acceptingOrders\n    menu {\n      items {\n        id\n        name\n        description\n        price {\n          amount\n          currency\n        }\n        available\n      }\n    }\n  }\n}": typeof types.ViewRestaurantDocument,
};
const documents: Documents = {
    "query BrowseRestaurants($first: Int!, $after: String) {\n  restaurants(first: $first, after: $after) {\n    edges {\n      cursor\n      node {\n        id\n        name\n        address\n        acceptingOrders\n      }\n    }\n    pageInfo {\n      hasNextPage\n      endCursor\n    }\n  }\n}": types.BrowseRestaurantsDocument,
    "query ManageRestaurant($id: ID!) {\n  restaurant(id: $id) {\n    id\n    name\n    address\n    acceptingOrders\n  }\n}\n\nmutation OpenRestaurant($restaurantId: ID!) {\n  openRestaurant(restaurantId: $restaurantId) {\n    changed\n    restaurant {\n      id\n      name\n      address\n      acceptingOrders\n    }\n  }\n}\n\nmutation CloseRestaurant($restaurantId: ID!) {\n  closeRestaurant(restaurantId: $restaurantId) {\n    changed\n    restaurant {\n      id\n      name\n      address\n      acceptingOrders\n    }\n  }\n}\n\nquery MyRestaurants($first: Int!) {\n  myRestaurants(first: $first) {\n    edges {\n      node {\n        id\n        name\n        address\n        acceptingOrders\n      }\n    }\n  }\n}": types.ManageRestaurantDocument,
    "mutation PlaceOrder($input: PlaceOrderInput!) {\n  placeOrder(input: $input) {\n    order {\n      id\n    }\n    problems {\n      code\n      menuItemIds\n    }\n  }\n}": types.PlaceOrderDocument,
    "query BffInfo {\n  bffInfo {\n    name\n    version\n  }\n}": types.BffInfoDocument,
    "query ViewOrder($id: ID!) {\n  order(id: $id) {\n    id\n    status\n    endReason\n    restaurant {\n      id\n      name\n    }\n    items {\n      menuItem {\n        id\n        name\n      }\n      quantity\n      unitPrice {\n        amount\n        currency\n      }\n      lineTotal {\n        amount\n        currency\n      }\n    }\n    total {\n      amount\n      currency\n    }\n    deliveryAddress\n    placedAt\n    milestones {\n      kind\n      occurredAt\n    }\n    cancellation {\n      availableUntil\n    }\n  }\n}\n\nmutation RequestOrderCancellation($input: RequestOrderCancellationInput!) {\n  requestOrderCancellation(input: $input) {\n    outcome\n    problem {\n      code\n    }\n  }\n}": types.ViewOrderDocument,
    "query ViewRestaurant($id: ID!) {\n  restaurant(id: $id) {\n    id\n    name\n    address\n    acceptingOrders\n    menu {\n      items {\n        id\n        name\n        description\n        price {\n          amount\n          currency\n        }\n        available\n      }\n    }\n  }\n}": types.ViewRestaurantDocument,
};

/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 *
 *
 * @example
 * ```ts
 * const query = graphql(`query GetUser($id: ID!) { user(id: $id) { name } }`);
 * ```
 *
 * The query argument is unknown!
 * Please regenerate the types.
 */
export function graphql(source: string): unknown;

/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "query BrowseRestaurants($first: Int!, $after: String) {\n  restaurants(first: $first, after: $after) {\n    edges {\n      cursor\n      node {\n        id\n        name\n        address\n        acceptingOrders\n      }\n    }\n    pageInfo {\n      hasNextPage\n      endCursor\n    }\n  }\n}"): (typeof documents)["query BrowseRestaurants($first: Int!, $after: String) {\n  restaurants(first: $first, after: $after) {\n    edges {\n      cursor\n      node {\n        id\n        name\n        address\n        acceptingOrders\n      }\n    }\n    pageInfo {\n      hasNextPage\n      endCursor\n    }\n  }\n}"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "query ManageRestaurant($id: ID!) {\n  restaurant(id: $id) {\n    id\n    name\n    address\n    acceptingOrders\n  }\n}\n\nmutation OpenRestaurant($restaurantId: ID!) {\n  openRestaurant(restaurantId: $restaurantId) {\n    changed\n    restaurant {\n      id\n      name\n      address\n      acceptingOrders\n    }\n  }\n}\n\nmutation CloseRestaurant($restaurantId: ID!) {\n  closeRestaurant(restaurantId: $restaurantId) {\n    changed\n    restaurant {\n      id\n      name\n      address\n      acceptingOrders\n    }\n  }\n}\n\nquery MyRestaurants($first: Int!) {\n  myRestaurants(first: $first) {\n    edges {\n      node {\n        id\n        name\n        address\n        acceptingOrders\n      }\n    }\n  }\n}"): (typeof documents)["query ManageRestaurant($id: ID!) {\n  restaurant(id: $id) {\n    id\n    name\n    address\n    acceptingOrders\n  }\n}\n\nmutation OpenRestaurant($restaurantId: ID!) {\n  openRestaurant(restaurantId: $restaurantId) {\n    changed\n    restaurant {\n      id\n      name\n      address\n      acceptingOrders\n    }\n  }\n}\n\nmutation CloseRestaurant($restaurantId: ID!) {\n  closeRestaurant(restaurantId: $restaurantId) {\n    changed\n    restaurant {\n      id\n      name\n      address\n      acceptingOrders\n    }\n  }\n}\n\nquery MyRestaurants($first: Int!) {\n  myRestaurants(first: $first) {\n    edges {\n      node {\n        id\n        name\n        address\n        acceptingOrders\n      }\n    }\n  }\n}"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "mutation PlaceOrder($input: PlaceOrderInput!) {\n  placeOrder(input: $input) {\n    order {\n      id\n    }\n    problems {\n      code\n      menuItemIds\n    }\n  }\n}"): (typeof documents)["mutation PlaceOrder($input: PlaceOrderInput!) {\n  placeOrder(input: $input) {\n    order {\n      id\n    }\n    problems {\n      code\n      menuItemIds\n    }\n  }\n}"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "query BffInfo {\n  bffInfo {\n    name\n    version\n  }\n}"): (typeof documents)["query BffInfo {\n  bffInfo {\n    name\n    version\n  }\n}"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "query ViewOrder($id: ID!) {\n  order(id: $id) {\n    id\n    status\n    endReason\n    restaurant {\n      id\n      name\n    }\n    items {\n      menuItem {\n        id\n        name\n      }\n      quantity\n      unitPrice {\n        amount\n        currency\n      }\n      lineTotal {\n        amount\n        currency\n      }\n    }\n    total {\n      amount\n      currency\n    }\n    deliveryAddress\n    placedAt\n    milestones {\n      kind\n      occurredAt\n    }\n    cancellation {\n      availableUntil\n    }\n  }\n}\n\nmutation RequestOrderCancellation($input: RequestOrderCancellationInput!) {\n  requestOrderCancellation(input: $input) {\n    outcome\n    problem {\n      code\n    }\n  }\n}"): (typeof documents)["query ViewOrder($id: ID!) {\n  order(id: $id) {\n    id\n    status\n    endReason\n    restaurant {\n      id\n      name\n    }\n    items {\n      menuItem {\n        id\n        name\n      }\n      quantity\n      unitPrice {\n        amount\n        currency\n      }\n      lineTotal {\n        amount\n        currency\n      }\n    }\n    total {\n      amount\n      currency\n    }\n    deliveryAddress\n    placedAt\n    milestones {\n      kind\n      occurredAt\n    }\n    cancellation {\n      availableUntil\n    }\n  }\n}\n\nmutation RequestOrderCancellation($input: RequestOrderCancellationInput!) {\n  requestOrderCancellation(input: $input) {\n    outcome\n    problem {\n      code\n    }\n  }\n}"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "query ViewRestaurant($id: ID!) {\n  restaurant(id: $id) {\n    id\n    name\n    address\n    acceptingOrders\n    menu {\n      items {\n        id\n        name\n        description\n        price {\n          amount\n          currency\n        }\n        available\n      }\n    }\n  }\n}"): (typeof documents)["query ViewRestaurant($id: ID!) {\n  restaurant(id: $id) {\n    id\n    name\n    address\n    acceptingOrders\n    menu {\n      items {\n        id\n        name\n        description\n        price {\n          amount\n          currency\n        }\n        available\n      }\n    }\n  }\n}"];

export function graphql(source: string) {
  return (documents as any)[source] ?? {};
}

export type DocumentType<TDocumentNode extends DocumentNode<any, any>> = TDocumentNode extends DocumentNode<  infer TType,  any>  ? TType  : never;