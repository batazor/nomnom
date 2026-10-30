// Mapper Domain Event → Integration Event of the Restaurant slice (ADR 25,
// ADR 31). Only opening facts and the wait level change have a contract in
// nomnom/restaurants/v1/events.proto (stream restaurants.v1.restaurant-events,
// key = restaurant id). RestaurantRegistered and RestaurantRenamed stay
// internal: no spec requires them outside the context, and staff changes are
// not Domain Events at all (SpiceDB relationships).
import {
  RestaurantClosedSchema,
  RestaurantOpenedSchema,
  RestaurantWaitLevelChangedSchema,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import {
  allowlistMapper,
  type EventAllowlist,
  protobufIntegrationEvents,
} from "../../../../infrastructure/outbox/integration-event.ts";
import type { OutboxEventMapper } from "../../../../infrastructure/repository/outbox.ts";
import { WAIT_TIME_AGGREGATE_TYPE } from "../../applications/record-wait-time/record-wait-time.ts";
import type { RestaurantEvent } from "../../domains/restaurant/events.ts";
import type { WaitLevelChanged } from "../../domains/restaurant/wait-time.ts";

export const RESTAURANT_EVENTS_TOPIC = "restaurants.v1.restaurant-events";
/** `aggregate_type` the Open/Close Restaurant use cases pass to the Outbox. */
export const RESTAURANT_AGGREGATE_TYPE = "Restaurant";

const integration = protobufIntegrationEvents(RESTAURANT_EVENTS_TOPIC);

/** Explicit allowlist: Domain Event type → Integration Event. */
const MAPPERS: EventAllowlist<RestaurantEvent> = {
  RestaurantOpened: (event) =>
    integration(RestaurantOpenedSchema, {
      restaurantId: event.restaurantId,
      marketId: event.marketId,
    }),
  RestaurantClosed: (event) =>
    integration(RestaurantClosedSchema, {
      restaurantId: event.restaurantId,
      marketId: event.marketId,
    }),
};

/** Integration Event types of the topic (full Protobuf message names). */
export const PUBLISHED_RESTAURANT_EVENT_TYPES: readonly string[] = [
  RestaurantOpenedSchema.typeName,
  RestaurantClosedSchema.typeName,
  RestaurantWaitLevelChangedSchema.typeName,
];

export const restaurantEventMapper: OutboxEventMapper = allowlistMapper(
  RESTAURANT_AGGREGATE_TYPE,
  MAPPERS,
);

/**
 * RestaurantWaitLevelChanged (FR-646) of the Wait Time Statistics: its own
 * aggregate type, versioned by the number of level changes, so its event ids
 * never collide with the Restaurant aggregate; same topic and key.
 */
export const restaurantWaitTimeEventMapper: OutboxEventMapper =
  allowlistMapper<WaitLevelChanged>(WAIT_TIME_AGGREGATE_TYPE, {
    RestaurantWaitLevelChanged: (event) =>
      integration(RestaurantWaitLevelChangedSchema, {
        restaurantId: event.restaurantId,
        marketId: event.marketId,
        elevated: event.elevated,
      }),
  });
