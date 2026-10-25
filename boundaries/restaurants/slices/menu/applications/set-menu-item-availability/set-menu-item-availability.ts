import type { Clock } from "../../../../pkg/application/clock.ts";
import type { Outbox } from "../../../../pkg/application/outbox.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import type { UnitOfWork } from "../../../../pkg/application/unit-of-work.ts";
import type { MenuRepository } from "../../domains/menu/menu.ts";
import type { MenuAccessControl } from "../access-control.ts";
import {
  authorizeMenu,
  loadMenu,
  type MenuItemChangeResult,
  requireMenuItem,
  toMenuItemView,
} from "../menu-support.ts";
import { saveAndPublish } from "../publish-menu-change.ts";

export type SetMenuItemAvailability = Readonly<{
  subject: Subject;
  restaurantId: string;
  menuItemId: string;
  isAvailable: boolean;
}>;

/**
 * Changes only Menu Item availability and publishes
 * `MenuItemAvailabilityChanged`; repeating the current value is a no-op.
 */
export class SetMenuItemAvailabilityHandler {
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

  async execute(
    command: SetMenuItemAvailability,
  ): Promise<MenuItemChangeResult> {
    await authorizeMenu(
      this.#accessControl,
      command.subject,
      command.restaurantId,
      "set menu item availability",
    );
    const now = this.#clock.now();
    return this.#unitOfWork.run(async () => {
      const menu = await loadMenu(this.#menus, command.restaurantId);
      requireMenuItem(menu, command.menuItemId);
      const changed = menu.setItemAvailability(
        command.menuItemId,
        command.isAvailable,
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
