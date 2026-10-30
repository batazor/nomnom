import type { Subject } from "../../pkg/authz/subject.ts";
import type { Outbox } from "../../pkg/outbox/outbox.ts";
import type { MenuAccessControl } from "../../slices/menu/applications/access-control.ts";
import { AddMenuItemHandler } from "../../slices/menu/applications/add-menu-item/add-menu-item.ts";
import { BatchGetMenusHandler } from "../../slices/menu/applications/batch-get-menus/batch-get-menus.ts";
import { CreateMenuHandler } from "../../slices/menu/applications/create-menu/create-menu.ts";
import { GetMenuHandler } from "../../slices/menu/applications/get-menu/get-menu.ts";
import { SetMenuItemAvailabilityHandler } from "../../slices/menu/applications/set-menu-item-availability/set-menu-item-availability.ts";
import { UpdateMenuItemHandler } from "../../slices/menu/applications/update-menu-item/update-menu-item.ts";
import {
  createMenuCollection,
  DatabaseMenuReader,
  DatabaseMenuRepository,
} from "../../slices/menu/infrastructure/repository/menus.ts";
import {
  createInfrastructure,
  createRestaurantWorld,
  type Infrastructure,
  owner,
  SequentialIdGenerator,
} from "./restaurants.ts";

export const createMenuWorld = async (
  infrastructure?: Infrastructure,
  options: { outbox?: Outbox } = {},
) => {
  infrastructure ??= await createInfrastructure();
  const restaurantWorld = await createRestaurantWorld(infrastructure);
  const store = createMenuCollection(
    infrastructure.db,
    infrastructure.unitOfWork,
  );
  const menus = new DatabaseMenuRepository(store);
  const reader = new DatabaseMenuReader(store);
  const accessControl = new TestMenuAccessControl();
  const ids = new SequentialIdGenerator("menu");
  const outbox = options.outbox ?? infrastructure.outbox;
  const createMenu = new CreateMenuHandler({
    accessControl,
    restaurants: restaurantWorld.restaurants,
    menus,
    ids,
    clock: infrastructure.clock,
  });
  const addMenuItem = new AddMenuItemHandler({
    accessControl,
    unitOfWork: infrastructure.unitOfWork,
    menus,
    outbox,
    ids,
    clock: infrastructure.clock,
  });
  const updateMenuItem = new UpdateMenuItemHandler({
    accessControl,
    unitOfWork: infrastructure.unitOfWork,
    menus,
    outbox,
    clock: infrastructure.clock,
  });
  const setAvailability = new SetMenuItemAvailabilityHandler({
    accessControl,
    unitOfWork: infrastructure.unitOfWork,
    menus,
    outbox,
    clock: infrastructure.clock,
  });
  const getMenu = new GetMenuHandler({ reader });
  const batchGetMenus = new BatchGetMenusHandler({ reader });

  /** Registers an open restaurant managed by {@link owner} with an empty menu. */
  const givenRestaurantWithMenu = async (
    name = "Pho Bar",
    state: { open: boolean; marketId?: string } = { open: true },
  ): Promise<string> => {
    const restaurantId = await restaurantWorld.givenRestaurant(name, state);
    accessControl.grantManager(owner, restaurantId);
    await createMenu.execute({ subject: owner, restaurantId });
    return restaurantId;
  };

  const givenMenuItem = async (
    restaurantId: string,
    name: string,
    isAvailable = true,
  ): Promise<string> => {
    const item = await addMenuItem.execute({
      subject: owner,
      restaurantId,
      name,
      price: { amount: "9.50", currency: "EUR" },
      isAvailable,
    });
    return item.menuItemId;
  };

  return {
    ...restaurantWorld,
    restaurantWorld,
    menuStore: store,
    menus,
    menuReader: reader,
    menuAccessControl: accessControl,
    createMenu,
    addMenuItem,
    updateMenuItem,
    setAvailability,
    getMenu,
    batchGetMenus,
    givenRestaurantWithMenu,
    givenMenuItem,
  };
};

/** Test authorization graph with explicit menu managers per restaurant. */
export class TestMenuAccessControl implements MenuAccessControl {
  readonly #managers = new Set<string>();

  grantManager(subject: Subject, restaurantId: string): this {
    this.#managers.add(`${subject.type}:${subject.id}@${restaurantId}`);
    return this;
  }

  async canManageMenu(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean> {
    return this.#managers.has(`${subject.type}:${subject.id}@${restaurantId}`);
  }
}
