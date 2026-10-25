import type { Clock } from "../../../../pkg/application/clock.ts";
import type { IdGenerator } from "../../../../pkg/application/id-generator.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import type { MenuRepository } from "../../domains/menu/menu.ts";
import { Money, type MoneyValue } from "../../domains/menu/vo/money.ts";
import type { MenuAccessControl } from "../access-control.ts";
import {
  authorizeMenu,
  loadMenu,
  type MenuItemChangeResult,
  toMenuItemView,
} from "../menu-support.ts";

export type AddMenuItem = Readonly<{
  subject: Subject;
  restaurantId: string;
  name: string;
  description?: string;
  price: MoneyValue;
  /** Defaults to `true`. */
  isAvailable?: boolean;
}>;

/** Adds a dish to the restaurant menu; no business fact is published. */
export class AddMenuItemHandler {
  readonly #accessControl: MenuAccessControl;
  readonly #menus: MenuRepository;
  readonly #ids: IdGenerator;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: MenuAccessControl;
    menus: MenuRepository;
    ids: IdGenerator;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#menus = dependencies.menus;
    this.#ids = dependencies.ids;
    this.#clock = dependencies.clock;
  }

  async execute(command: AddMenuItem): Promise<MenuItemChangeResult> {
    await authorizeMenu(
      this.#accessControl,
      command.subject,
      command.restaurantId,
      "add menu item",
    );
    const price = Money.from(command.price);
    const menu = await loadMenu(this.#menus, command.restaurantId);
    const item = menu.addItem({
      id: this.#ids.next(),
      name: command.name,
      description: command.description,
      price,
      isAvailable: command.isAvailable ?? true,
      addedAt: this.#clock.now(),
    });
    const version = await this.#menus.save(menu);
    menu.pullEvents();
    return { ...toMenuItemView(item), menuId: menu.id, version, changed: true };
  }
}
