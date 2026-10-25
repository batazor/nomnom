// Mapper Domain Event → Integration Event of the Menu slice (ADR 25, ADR 31).
// Only MenuItemAvailabilityChanged has a contract in
// nomnom/restaurants/v1/events.proto (stream restaurants.v1.menu-events,
// key = menu id, the Menu aggregate). MenuCreated, MenuItemAdded and
// MenuItemUpdated stay internal: menu content is read through GetMenu, and
// only availability is a fact other contexts react to.
import { MenuItemAvailabilityChangedSchema } from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import type { OutboxEventMapper } from "../../../../pkg/infrastructure/mongodb/outbox.ts";
import {
  allowlistMapper,
  type EventAllowlist,
  protobufIntegrationEvents,
} from "../../../../pkg/infrastructure/outbox/integration-event.ts";
import type { MenuEvent } from "../../domains/menu/events.ts";

export const MENU_EVENTS_TOPIC = "restaurants.v1.menu-events";
/** `aggregate_type` the menu use cases pass to the Outbox. */
export const MENU_AGGREGATE_TYPE = "Menu";

const integration = protobufIntegrationEvents(MENU_EVENTS_TOPIC);

/** Explicit allowlist: Domain Event type → Integration Event. */
const MAPPERS: EventAllowlist<MenuEvent> = {
  MenuItemAvailabilityChanged: (event) =>
    integration(MenuItemAvailabilityChangedSchema, {
      menuId: event.menuId,
      restaurantId: event.restaurantId,
      menuItemId: event.menuItemId,
      available: event.isAvailable,
    }),
};

/** Integration Event types of the topic (full Protobuf message names). */
export const PUBLISHED_MENU_EVENT_TYPES: readonly string[] = [
  MenuItemAvailabilityChangedSchema.typeName,
];

export const menuEventMapper: OutboxEventMapper = allowlistMapper(
  MENU_AGGREGATE_TYPE,
  MAPPERS,
);
