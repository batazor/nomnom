// Mapper Domain Event → Integration Event of the Restaurant slice (ADR 25,
// ADR 31). Only opening facts have a contract in
// nomnom/restaurants/v1/events.proto (stream restaurants.v1.restaurant-events,
// key = restaurant id). RestaurantRegistered, RestaurantRenamed and
// RestaurantAddressChanged stay internal: no spec requires them outside the
// context, and staff changes are not Domain Events at all (SpiceDB
// relationships).
import {
  RestaurantClosedSchema,
  RestaurantOpenedSchema,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import type { OutboxEventMapper } from "../../../../pkg/infrastructure/mongodb/outbox.ts";
import {
  allowlistMapper,
  type EventAllowlist,
  protobufIntegrationEvents,
} from "../../../../pkg/infrastructure/outbox/integration-event.ts";
import type { RestaurantEvent } from "../../domains/restaurant/events.ts";

export const RESTAURANT_EVENTS_TOPIC = "restaurants.v1.restaurant-events";
/** `aggregate_type` the Open/Close Restaurant use cases pass to the Outbox. */
export const RESTAURANT_AGGREGATE_TYPE = "Restaurant";

const integration = protobufIntegrationEvents(RESTAURANT_EVENTS_TOPIC);

/** Explicit allowlist: Domain Event type → Integration Event. */
const MAPPERS: EventAllowlist<RestaurantEvent> = {
  RestaurantOpened: (event) =>
    integration(RestaurantOpenedSchema, { restaurantId: event.restaurantId }),
  RestaurantClosed: (event) =>
    integration(RestaurantClosedSchema, { restaurantId: event.restaurantId }),
};

/** Integration Event types of the topic (full Protobuf message names). */
export const PUBLISHED_RESTAURANT_EVENT_TYPES: readonly string[] = [
  RestaurantOpenedSchema.typeName,
  RestaurantClosedSchema.typeName,
];

export const restaurantEventMapper: OutboxEventMapper = allowlistMapper(
  RESTAURANT_AGGREGATE_TYPE,
  MAPPERS,
);
