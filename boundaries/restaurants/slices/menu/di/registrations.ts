// Awilix registrations of the `menu` slice (ADR 5): MongoDB adapters of its
// ports, the Outbox mapper of the Menu aggregate, the public Get Menu and
// Batch Get Menus queries and the menu management commands served by gRPC.
import { type AwilixContainer, asFunction, asValue } from "awilix";
import type { Valkey } from "iovalkey";
import type { Db } from "mongodb";
import type { Registrations } from "../../../di/container.ts";
import type { OutboxEventMapper } from "../../../infrastructure/repository/outbox.ts";
import type { DatabaseUnitOfWork } from "../../../infrastructure/repository/unit-of-work.ts";
import type { CacheMetrics } from "../../../infrastructure/valkey/read-cache.ts";
import type { ValkeyConfig } from "../../../pkg/config/config.ts";
import type { Outbox } from "../../../pkg/outbox/outbox.ts";
import {
  command,
  query,
  type UseCase,
} from "../../../pkg/telemetry/usecase.ts";
import type { Clock } from "../../../pkg/usecase/clock.ts";
import type { IdGenerator } from "../../../pkg/usecase/id-generator.ts";
import type { RestaurantRepository } from "../../restaurant/domains/restaurant/restaurant.ts";
import type { MenuAccessControl } from "../applications/access-control/access-control.ts";
import { AddMenuItemHandler } from "../applications/add-menu-item/add-menu-item.ts";
import { BatchGetMenusHandler } from "../applications/batch-get-menus/batch-get-menus.ts";
import { CreateMenuHandler } from "../applications/create-menu/create-menu.ts";
import { GetMenuHandler } from "../applications/get-menu/get-menu.ts";
import { SetMenuItemAvailabilityHandler } from "../applications/set-menu-item-availability/set-menu-item-availability.ts";
import { UpdateMenuItemHandler } from "../applications/update-menu-item/update-menu-item.ts";
import type { MenuRepository } from "../domains/menu/menu.ts";
import { CachedMenuReader } from "../infrastructure/cache/menus.ts";
import { menuEventMapper } from "../infrastructure/integration/menu-events.ts";
import {
  createMenuCollection,
  DatabaseMenuReader,
  DatabaseMenuRepository,
} from "../infrastructure/repository/menus.ts";

/** Application-scope dependencies the slice takes from the process. */
export interface MenuDependencies {
  readonly db: Db;
  readonly unitOfWork: DatabaseUnitOfWork;
  readonly clock: Clock;
  readonly ids: IdGenerator;
  readonly outbox: Outbox;
  readonly menuAccessControl: MenuAccessControl;
  /** Create Menu requires an existing restaurant (FR-211). */
  readonly restaurants: RestaurantRepository;
  /** ADR 43: Get Menu is cached in Valkey for `valkeyConfig.ttlMs`. */
  readonly valkey: Valkey;
  readonly valkeyConfig: ValkeyConfig;
  readonly cacheMetrics: CacheMetrics;
}

/** What the slice provides to the process and to other slices. */
export interface MenuCradle {
  /**
   * Only `MenuItemAvailabilityChanged` of the Menu aggregate is written to the
   * Outbox (explicit allowlist, topic restaurants.v1.menu-events).
   */
  readonly menuOutboxEventMapper: OutboxEventMapper;
  readonly menus: MenuRepository;
  /** Get Menu and Batch Get Menus through the Valkey read cache (ADR 43). */
  readonly cachedMenuReader: CachedMenuReader;
  readonly getMenu: GetMenuHandler;
  readonly batchGetMenus: BatchGetMenusHandler;
  readonly createMenu: CreateMenuHandler;
  readonly addMenuItem: AddMenuItemHandler;
  readonly updateMenuItem: UpdateMenuItemHandler;
  readonly setMenuItemAvailability: SetMenuItemAvailabilityHandler;
}

/** Use cases of the slice with their telemetry names, by cradle key. */
export const MENU_USE_CASES = {
  getMenu: query("get_menu"),
  batchGetMenus: query("batch_get_menus"),
  createMenu: command("create_menu"),
  addMenuItem: command("add_menu_item"),
  updateMenuItem: command("update_menu_item"),
  setMenuItemAvailability: command("set_menu_item_availability"),
} as const satisfies Partial<Record<keyof MenuCradle, UseCase>>;

export const registerMenu = (
  container: AwilixContainer<MenuDependencies & MenuCradle>,
): void => {
  const registrations: Registrations<MenuCradle> = {
    menuOutboxEventMapper: asValue(menuEventMapper),
    menus: asFunction(
      ({ db, unitOfWork }: MenuDependencies) =>
        new DatabaseMenuRepository(createMenuCollection(db, unitOfWork)),
    ).singleton(),
    cachedMenuReader: asFunction(
      ({
        db,
        unitOfWork,
        valkey,
        valkeyConfig,
        cacheMetrics,
      }: MenuDependencies) =>
        new CachedMenuReader(
          new DatabaseMenuReader(createMenuCollection(db, unitOfWork)),
          {
            client: valkey,
            ttlMs: valkeyConfig.ttlMs,
            metrics: cacheMetrics,
          },
        ),
    ).singleton(),
    getMenu: asFunction(
      ({ cachedMenuReader }: MenuCradle) =>
        new GetMenuHandler({ reader: cachedMenuReader }),
    ).singleton(),
    batchGetMenus: asFunction(
      ({ cachedMenuReader }: MenuCradle) =>
        new BatchGetMenusHandler({ reader: cachedMenuReader }),
    ).singleton(),
    createMenu: asFunction(
      ({
        menuAccessControl,
        restaurants,
        menus,
        ids,
        clock,
      }: MenuDependencies & MenuCradle) =>
        new CreateMenuHandler({
          accessControl: menuAccessControl,
          restaurants,
          menus,
          ids,
          clock,
        }),
    ).singleton(),
    addMenuItem: asFunction(
      ({
        menuAccessControl,
        unitOfWork,
        menus,
        outbox,
        ids,
        clock,
      }: MenuDependencies & MenuCradle) =>
        new AddMenuItemHandler({
          accessControl: menuAccessControl,
          unitOfWork,
          menus,
          outbox,
          ids,
          clock,
        }),
    ).singleton(),
    updateMenuItem: asFunction(
      ({
        menuAccessControl,
        unitOfWork,
        menus,
        outbox,
        clock,
      }: MenuDependencies & MenuCradle) =>
        new UpdateMenuItemHandler({
          accessControl: menuAccessControl,
          unitOfWork,
          menus,
          outbox,
          clock,
        }),
    ).singleton(),
    setMenuItemAvailability: asFunction(
      ({
        menuAccessControl,
        unitOfWork,
        menus,
        outbox,
        clock,
      }: MenuDependencies & MenuCradle) =>
        new SetMenuItemAvailabilityHandler({
          accessControl: menuAccessControl,
          unitOfWork,
          menus,
          outbox,
          clock,
        }),
    ).singleton(),
  };
  container.register(registrations);
};
