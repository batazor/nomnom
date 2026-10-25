import type { Outbox } from "../../../pkg/application/outbox.ts";
import type { Menu, MenuRepository } from "../domains/menu/menu.ts";

/** Business facts of the menu that leave the context through the Outbox. */
const PUBLISHED_MENU_EVENTS: ReadonlySet<string> = new Set([
  "MenuItemAvailabilityChanged",
]);

/** Saves the menu and passes its published facts to the Outbox in the active Unit of Work. */
export const saveAndPublish = async (
  menus: MenuRepository,
  outbox: Outbox,
  menu: Menu,
): Promise<number> => {
  const version = await menus.save(menu);
  const published = menu
    .pullEvents()
    .filter((event) => PUBLISHED_MENU_EVENTS.has(event.type));
  if (published.length > 0) {
    await outbox.add({ type: "Menu", id: menu.id, version }, published);
  }
  return version;
};
