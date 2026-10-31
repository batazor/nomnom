import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { Outbox } from "../../../../pkg/outbox/outbox.ts";
import type { UnitOfWork } from "../../../../pkg/unit-of-work/unit-of-work.ts";
import type { Clock } from "../../../../pkg/usecase/clock.ts";
import {
  Money,
  type MoneyValue,
} from "../../../restaurant/domains/restaurant/vo/money.ts";
import type { MenuRepository } from "../../domains/menu/menu.ts";
import type { MenuAccessControl } from "../access-control/access-control.ts";
import {
  loadMenu,
  type MenuItemChangeResult,
  requireMenuItem,
  toMenuItemView,
} from "../menu-support/menu-support.ts";
import { saveAndPublish } from "../publish-change/publish-change.ts";

export type UpdateMenuItem = Readonly<{
  subject: Subject;
  restaurantId: string;
  menuItemId: string;
  name?: string;
  /** `null` removes the description. */
  description?: string | null;
  price?: MoneyValue;
  isAvailable?: boolean;
}>;

/**
 * Changes basic Menu Item data within menu invariants. A change of
 * availability publishes `MenuItemAvailabilityChanged` atomically with the menu.
 */
export class UpdateMenuItemHandler {
  readonly #accessControl: MenuAccessControl;
  readonly #unitOfWork: UnitOfWork;
  readonly #menus: MenuRepository;
  readonly #outbox: Outbox;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: MenuAccessControl;
    unitOfWork: UnitOfWork;
    menus: MenuRepository;
    outbox: Outbox;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#unitOfWork = dependencies.unitOfWork;
    this.#menus = dependencies.menus;
    this.#outbox = dependencies.outbox;
    this.#clock = dependencies.clock;
  }

  async execute(command: UpdateMenuItem): Promise<MenuItemChangeResult> {
    await requirePermission(
      this.#accessControl.canManageMenu(command.subject, command.restaurantId),
      "update menu item",
    );
    const price =
      command.price === undefined ? undefined : Money.from(command.price);
    const now = this.#clock.now();
    return this.#unitOfWork.run(async () => {
      const menu = await loadMenu(this.#menus, command.restaurantId);
      requireMenuItem(menu, command.menuItemId);
      const changed = menu.updateItem(
        command.menuItemId,
        {
          name: command.name,
          description: command.description,
          price,
          isAvailable: command.isAvailable,
        },
        now,
      );
      const version = changed
        ? await saveAndPublish(this.#menus, this.#outbox, menu)
        : menu.version;
      return {
        ...toMenuItemView(requireMenuItem(menu, command.menuItemId)),
        menuId: menu.id,
        version,
        changed,
      };
    });
  }
}
