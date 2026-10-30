// Mapper Domain Event → Integration Event of the Kitchen Order slice (ADR 25,
// ADR 31). Only the allowlisted facts below have a contract in
// nomnom/restaurants/v1/events.proto; every other Domain Event (local facts,
// Restaurant and Menu events) is skipped and never reaches the Outbox.
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import {
  PreparationDelayedSchema,
  PreparationFailedSchema,
  PreparationStartedSchema,
  RestaurantOrderAcceptedSchema,
  RestaurantOrderCancelledSchema,
  RestaurantOrderReadySchema,
  RestaurantOrderRejectedSchema,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import {
  allowlistMapper,
  type EventAllowlist,
  protobufIntegrationEvents,
} from "../../../../infrastructure/outbox/integration-event.ts";
import type { OutboxEventMapper } from "../../../../infrastructure/repository/outbox.ts";
import type { RestaurantOrderEvent } from "../../domains/restaurantorder/restaurantorder.ts";

export const RESTAURANT_ORDER_EVENTS_TOPIC =
  "restaurants.v1.restaurant-order-events";
export const RESTAURANT_ORDER_AGGREGATE_TYPE = "RestaurantOrder";

const integration = protobufIntegrationEvents(RESTAURANT_ORDER_EVENTS_TOPIC);

/** Explicit allowlist: Domain Event type → Integration Event. */
const MAPPERS: EventAllowlist<RestaurantOrderEvent> = {
  RestaurantOrderAccepted: (event) =>
    integration(RestaurantOrderAcceptedSchema, {
      orderId: event.orderId,
      expectedReadyAt: timestampFromDate(event.expectedReadyAt),
      marketId: event.marketId,
      restaurantId: event.restaurantId,
      // Only an order queued in the Unit of Work of its Acceptance (FR-644).
      ...(event.queuedAt === undefined
        ? {}
        : { queuedAt: timestampFromDate(event.queuedAt) }),
    }),
  RestaurantOrderRejected: (event) =>
    integration(RestaurantOrderRejectedSchema, {
      orderId: event.orderId,
      reason: event.reason ?? "",
      marketId: event.marketId,
    }),
  PreparationStarted: (event) =>
    integration(PreparationStartedSchema, {
      orderId: event.orderId,
      marketId: event.marketId,
      restaurantId: event.restaurantId,
      ...(event.queuedAt === undefined
        ? {}
        : { queuedAt: timestampFromDate(event.queuedAt) }),
    }),
  PreparationDelayed: (event) =>
    integration(PreparationDelayedSchema, {
      orderId: event.orderId,
      expectedReadyAt: timestampFromDate(event.expectedReadyAt),
      marketId: event.marketId,
    }),
  RestaurantOrderFailed: (event) =>
    integration(PreparationFailedSchema, {
      orderId: event.orderId,
      reason: event.reason,
      marketId: event.marketId,
      restaurantId: event.restaurantId,
    }),
  RestaurantOrderReady: (event) =>
    integration(RestaurantOrderReadySchema, {
      orderId: event.orderId,
      marketId: event.marketId,
      restaurantId: event.restaurantId,
    }),
  RestaurantOrderCancelled: (event) =>
    integration(RestaurantOrderCancelledSchema, {
      orderId: event.orderId,
      reason: event.reason ?? "",
      preparationStarted: event.preparationStarted,
      marketId: event.marketId,
      restaurantId: event.restaurantId,
    }),
};

/** Integration Event types of the topic (full Protobuf message names). */
export const PUBLISHED_RESTAURANT_ORDER_EVENT_TYPES: readonly string[] = [
  RestaurantOrderAcceptedSchema.typeName,
  RestaurantOrderRejectedSchema.typeName,
  PreparationStartedSchema.typeName,
  PreparationDelayedSchema.typeName,
  PreparationFailedSchema.typeName,
  RestaurantOrderReadySchema.typeName,
  RestaurantOrderCancelledSchema.typeName,
];

export const restaurantOrderEventMapper: OutboxEventMapper = allowlistMapper(
  RESTAURANT_ORDER_AGGREGATE_TYPE,
  MAPPERS,
);
