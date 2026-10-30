import type { Outbox } from "../../../pkg/outbox/outbox.ts";
import { publishAggregateChange } from "../../../pkg/outbox/publish-aggregate-change.ts";
import type { Menu, MenuRepository } from "../domains/menu/menu.ts";

/** Business facts of the menu that leave the context through the Outbox. */
const PUBLISHED_MENU_EVENTS: readonly string[] = [
  "MenuItemAdded",
  "MenuItemUpdated",
  "MenuItemAvailabilityChanged",
];

/** Saves the menu and passes its published facts to the Outbox in the active Unit of Work. */
export const saveAndPublish = (
  menus: MenuRepository,
  outbox: Outbox,
  menu: Menu,
): Promise<number> =>
  publishAggregateChange(menus, outbox, "Menu", PUBLISHED_MENU_EVENTS, menu);
