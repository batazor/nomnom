import type { Outbox } from "../../pkg/application/outbox.ts";
import { SequentialIdGenerator } from "../../pkg/infrastructure/memory/id-generator.ts";
import { AddMenuItemHandler } from "../../slices/menu/applications/add-menu-item/add-menu-item.ts";
import { CreateMenuHandler } from "../../slices/menu/applications/create-menu/create-menu.ts";
import { GetMenuHandler } from "../../slices/menu/applications/get-menu/get-menu.ts";
import { SetMenuItemAvailabilityHandler } from "../../slices/menu/applications/set-menu-item-availability/set-menu-item-availability.ts";
import { UpdateMenuItemHandler } from "../../slices/menu/applications/update-menu-item/update-menu-item.ts";
import {
  createMenuStore,
  InMemoryMenuAccessControl,
  InMemoryMenuReader,
  InMemoryMenuRepository,
} from "../../slices/menu/infrastructure/memory/menus.ts";
import {
  createInfrastructure,
  createRestaurantWorld,
  type Infrastructure,
  owner,
} from "./restaurants.ts";

export const createMenuWorld = (
  infrastructure: Infrastructure = createInfrastructure(),
  options: { outbox?: Outbox } = {},
) => {
  const restaurantWorld = createRestaurantWorld(infrastructure);
  const store = createMenuStore();
  infrastructure.unitOfWork.enlist(store);
  const menus = new InMemoryMenuRepository(store);
  const reader = new InMemoryMenuReader(store);
  const accessControl = new InMemoryMenuAccessControl();
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
    menus,
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

  /** Registers an open restaurant managed by {@link owner} with an empty menu. */
  const givenRestaurantWithMenu = async (
    name = "Pho Bar",
    state: { open: boolean } = { open: true },
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
    givenRestaurantWithMenu,
    givenMenuItem,
  };
};
