import {
  NotFoundError,
  PermissionDeniedError,
} from "../../../pkg/application/errors.ts";
import type { Subject } from "../../../pkg/application/subject.ts";
import type { Menu, MenuItem, MenuRepository } from "../domains/menu/menu.ts";
import type { MoneyValue } from "../domains/menu/vo/money.ts";
import type { MenuAccessControl } from "./access-control.ts";

export type MenuItemView = Readonly<{
  menuItemId: string;
  name: string;
  description: string | undefined;
  price: MoneyValue;
  isAvailable: boolean;
}>;

export type MenuView = Readonly<{
  menuId: string;
  restaurantId: string;
  items: readonly MenuItemView[];
}>;

/** Result of commands that change one Menu Item. */
export type MenuItemChangeResult = MenuItemView &
  Readonly<{
    menuId: string;
    version: number;
    changed: boolean;
  }>;

export const toMenuItemView = (item: MenuItem): MenuItemView => ({
  menuItemId: item.id,
  name: item.name,
  description: item.description,
  price: item.price.toValue(),
  isAvailable: item.isAvailable,
});

export const authorizeMenu = async (
  accessControl: MenuAccessControl,
  subject: Subject,
  restaurantId: string,
  action: string,
): Promise<void> => {
  if (!(await accessControl.canManageMenu(subject, restaurantId))) {
    throw new PermissionDeniedError(action);
  }
};

export const loadMenu = async (
  menus: MenuRepository,
  restaurantId: string,
): Promise<Menu> => {
  const menu = await menus.findByRestaurantId(restaurantId);
  if (menu === undefined) {
    throw new NotFoundError("Menu", restaurantId);
  }
  return menu;
};

export const requireMenuItem = (menu: Menu, menuItemId: string): MenuItem => {
  const item = menu.findItem(menuItemId);
  if (item === undefined) {
    throw new NotFoundError("MenuItem", menuItemId);
  }
  return item;
};
