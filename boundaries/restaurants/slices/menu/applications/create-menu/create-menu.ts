import type { Clock } from "../../../../pkg/application/clock.ts";
import {
  AlreadyExistsError,
  NotFoundError,
} from "../../../../pkg/application/errors.ts";
import type { IdGenerator } from "../../../../pkg/application/id-generator.ts";
import { requirePermission } from "../../../../pkg/application/require-permission.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import type { RestaurantRepository } from "../../../restaurant/domains/restaurant/restaurant.ts";
import { Menu, type MenuRepository } from "../../domains/menu/menu.ts";
import type { MenuAccessControl } from "../access-control.ts";

export type CreateMenu = Readonly<{
  subject: Subject;
  restaurantId: string;
}>;

export type CreateMenuResult = Readonly<{
  menuId: string;
  restaurantId: string;
  version: number;
}>;

/** Creates the single, initially empty menu of an existing restaurant. */
export class CreateMenuHandler {
  readonly #accessControl: MenuAccessControl;
  readonly #restaurants: RestaurantRepository;
  readonly #menus: MenuRepository;
  readonly #ids: IdGenerator;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: MenuAccessControl;
    restaurants: RestaurantRepository;
    menus: MenuRepository;
    ids: IdGenerator;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#restaurants = dependencies.restaurants;
    this.#menus = dependencies.menus;
    this.#ids = dependencies.ids;
    this.#clock = dependencies.clock;
  }

  async execute(command: CreateMenu): Promise<CreateMenuResult> {
    await requirePermission(
      this.#accessControl.canManageMenu(command.subject, command.restaurantId),
      "create menu",
    );
    if ((await this.#restaurants.get(command.restaurantId)) === undefined) {
      throw new NotFoundError("Restaurant", command.restaurantId);
    }
    if (
      (await this.#menus.findByRestaurantId(command.restaurantId)) !== undefined
    ) {
      throw new AlreadyExistsError("Menu", command.restaurantId);
    }
    const menu = Menu.create({
      id: this.#ids.next(),
      restaurantId: command.restaurantId,
      createdAt: this.#clock.now(),
    });
    const version = await this.#menus.save(menu);
    menu.pullEvents();
    return { menuId: menu.id, restaurantId: menu.restaurantId, version };
  }
}
