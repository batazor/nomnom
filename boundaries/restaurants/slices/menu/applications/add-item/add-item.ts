import { randomUUID } from "node:crypto";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { Outbox } from "../../../../pkg/outbox/outbox.ts";
import type { UnitOfWork } from "../../../../pkg/unit-of-work/unit-of-work.ts";
import {
  Money,
  type MoneyValue,
} from "../../../restaurant/domains/restaurant/vo/money.ts";
import type { MenuRepository } from "../../domains/menu/menu.ts";
import type { MenuAccessControl } from "../access-control/access-control.ts";
import type { Clock } from "../clock/clock.ts";
import { PermissionDeniedError } from "../failure/failure.ts";
import {
  loadMenu,
  type MenuItemChangeResult,
  toMenuItemView,
} from "../menu-support/menu-support.ts";
import { saveAndPublish } from "../publish-change/publish-change.ts";

export type AddMenuItem = Readonly<{
  subject: Subject;
  restaurantId: string;
  name: string;
  description?: string;
  price: MoneyValue;
  /** Defaults to `true`. */
  isAvailable?: boolean;
}>;

/** Adds a dish to the restaurant menu and publishes MenuItemAdded. */
export class AddMenuItemHandler {
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

  async execute(command: AddMenuItem): Promise<MenuItemChangeResult> {
    if (
      !(await this.#accessControl.canManageMenu(
        command.subject,
        command.restaurantId,
      ))
    ) {
      throw new PermissionDeniedError("add menu item");
    }
    const price = Money.from(command.price);
    return this.#unitOfWork.run(async () => {
      const menu = await loadMenu(this.#menus, command.restaurantId);
      const item = menu.addItem({
        id: randomUUID(),
        name: command.name,
        description: command.description,
        price,
        isAvailable: command.isAvailable ?? true,
        addedAt: this.#clock.now(),
      });
      const version = await saveAndPublish(this.#menus, this.#outbox, menu);
      return {
        ...toMenuItemView(item),
        menuId: menu.id,
        version,
        changed: true,
      };
    });
  }
}
