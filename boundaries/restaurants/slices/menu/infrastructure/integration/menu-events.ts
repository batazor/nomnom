// Mapper Domain Event → Integration Event of the Menu slice (ADR 25, ADR 31).
// MenuItemAdded, MenuItemUpdated and MenuItemAvailabilityChanged have a
// contract in nomnom/restaurants/v1/events.proto (stream
// restaurants.v1.menu-events, key = menu id, the Menu aggregate): they carry
// keys only, the content is read through GetMenu. The read cache of GetMenu is
// refreshed by them (ADR 43). MenuCreated stays internal: an absent menu is
// not cached.
import {
  MenuItemAddedSchema,
  MenuItemAvailabilityChangedSchema,
  MenuItemUpdatedSchema,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import {
  allowlistMapper,
  type EventAllowlist,
  protobufIntegrationEvents,
} from "../../../../infrastructure/outbox/integration-event.ts";
import type { OutboxEventMapper } from "../../../../infrastructure/repository/outbox.ts";
import type { MenuEvent } from "../../domains/menu/events.ts";

export const MENU_EVENTS_TOPIC = "restaurants.v1.menu-events";
/** `aggregate_type` the menu use cases pass to the Outbox. */
export const MENU_AGGREGATE_TYPE = "Menu";

const integration = protobufIntegrationEvents(MENU_EVENTS_TOPIC);

/** Explicit allowlist: Domain Event type → Integration Event. */
const MAPPERS: EventAllowlist<MenuEvent> = {
  MenuItemAdded: (event) =>
    integration(MenuItemAddedSchema, {
      menuId: event.menuId,
      restaurantId: event.restaurantId,
      menuItemId: event.menuItemId,
      marketId: event.marketId,
    }),
  MenuItemUpdated: (event) =>
    integration(MenuItemUpdatedSchema, {
      menuId: event.menuId,
      restaurantId: event.restaurantId,
      menuItemId: event.menuItemId,
      marketId: event.marketId,
    }),
  MenuItemAvailabilityChanged: (event) =>
    integration(MenuItemAvailabilityChangedSchema, {
      menuId: event.menuId,
      restaurantId: event.restaurantId,
      menuItemId: event.menuItemId,
      available: event.isAvailable,
      marketId: event.marketId,
    }),
};

/** Integration Event types of the topic (full Protobuf message names). */
export const PUBLISHED_MENU_EVENT_TYPES: readonly string[] = [
  MenuItemAddedSchema.typeName,
  MenuItemUpdatedSchema.typeName,
  MenuItemAvailabilityChangedSchema.typeName,
];

export const menuEventMapper: OutboxEventMapper = allowlistMapper(
  MENU_AGGREGATE_TYPE,
  MAPPERS,
);
