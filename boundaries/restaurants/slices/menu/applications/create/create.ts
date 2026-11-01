import { randomUUID } from "node:crypto";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { RestaurantRepository } from "../../../restaurant/domains/restaurant/restaurant.ts";
import { Menu, type MenuRepository } from "../../domains/menu/menu.ts";
import type { MenuAccessControl } from "../access-control/access-control.ts";
import type { Clock } from "../clock/clock.ts";
import {
  AlreadyExistsError,
  NotFoundError,
  PermissionDeniedError,
} from "../failure/failure.ts";

export type CreateMenu = Readonly<{
  subject: Subject;
  restaurantId: string;
}>;

export type CreateMenuResult = Readonly<{
  menuId: string;
  restaurantId: string;
  version: number;
}>;

/**
 * Creates the single, initially empty menu of an existing restaurant, in the
 * Market of that restaurant (ADR 41).
 */
export class CreateMenuHandler {
  readonly #accessControl: MenuAccessControl;
  readonly #restaurants: RestaurantRepository;
  readonly #menus: MenuRepository;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: MenuAccessControl;
    restaurants: RestaurantRepository;
    menus: MenuRepository;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#restaurants = dependencies.restaurants;
    this.#menus = dependencies.menus;
    this.#clock = dependencies.clock;
  }

  async execute(command: CreateMenu): Promise<CreateMenuResult> {
    if (
      !(await this.#accessControl.canManageMenu(
        command.subject,
        command.restaurantId,
      ))
    ) {
      throw new PermissionDeniedError("create menu");
    }
    const restaurant = await this.#restaurants.get(command.restaurantId);
    if (restaurant === undefined) {
      throw new NotFoundError("Restaurant", command.restaurantId);
    }
    if (
      (await this.#menus.findByRestaurantId(command.restaurantId)) !== undefined
    ) {
      throw new AlreadyExistsError("Menu", command.restaurantId);
    }
    const menu = Menu.create({
      id: randomUUID(),
      restaurantId: command.restaurantId,
      marketId: restaurant.marketId.value,
      createdAt: this.#clock.now(),
    });
    const version = await this.#menus.save(menu);
    menu.pullEvents();
    return { menuId: menu.id, restaurantId: menu.restaurantId, version };
  }
}
