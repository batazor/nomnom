import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { Outbox } from "../../../../pkg/outbox/outbox.ts";
import type { UnitOfWork } from "../../../../pkg/unit-of-work/unit-of-work.ts";
import type { Clock } from "../../../../pkg/usecase/clock.ts";
import type { IdGenerator } from "../../../../pkg/usecase/id-generator.ts";
import type { MenuRepository } from "../../domains/menu/menu.ts";
import { Money, type MoneyValue } from "../../domains/menu/vo/money.ts";
import type { MenuAccessControl } from "../access-control.ts";
import {
  loadMenu,
  type MenuItemChangeResult,
  toMenuItemView,
} from "../menu-support.ts";
import { saveAndPublish } from "../publish-menu-change.ts";

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
  readonly #ids: IdGenerator;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: MenuAccessControl;
    unitOfWork: UnitOfWork;
    menus: MenuRepository;
    outbox: Outbox;
    ids: IdGenerator;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#unitOfWork = dependencies.unitOfWork;
    this.#menus = dependencies.menus;
    this.#outbox = dependencies.outbox;
    this.#ids = dependencies.ids;
    this.#clock = dependencies.clock;
  }

  async execute(command: AddMenuItem): Promise<MenuItemChangeResult> {
    await requirePermission(
      this.#accessControl.canManageMenu(command.subject, command.restaurantId),
      "add menu item",
    );
    const price = Money.from(command.price);
    return this.#unitOfWork.run(async () => {
      const menu = await loadMenu(this.#menus, command.restaurantId);
      const item = menu.addItem({
        id: this.#ids.next(),
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
