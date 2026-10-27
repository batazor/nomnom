// Awilix registrations of the `menu` slice (ADR 5): MongoDB adapters of its
// ports, the Outbox mapper of the Menu aggregate, the public Get Menu query
// and the menu management commands served by gRPC.
import { type AwilixContainer, asFunction, asValue } from "awilix";
import type { Valkey } from "iovalkey";
import type { Db } from "mongodb";

import type { Clock } from "../../../pkg/application/clock.ts";
import type { IdGenerator } from "../../../pkg/application/id-generator.ts";
import type { Outbox } from "../../../pkg/application/outbox.ts";
import type { ValkeyEnabledConfig } from "../../../pkg/config/config.ts";
import type { Registrations } from "../../../pkg/di/container.ts";
import type { OutboxEventMapper } from "../../../pkg/infrastructure/repository/outbox.ts";
import type { DatabaseUnitOfWork } from "../../../pkg/infrastructure/repository/unit-of-work.ts";
import type { CacheMetrics } from "../../../pkg/infrastructure/valkey/read-cache.ts";
import type { RestaurantRepository } from "../../restaurant/domains/restaurant/restaurant.ts";
import type { MenuAccessControl } from "../applications/access-control.ts";
import { AddMenuItemHandler } from "../applications/add-menu-item/add-menu-item.ts";
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
  readonly valkeyConfig: ValkeyEnabledConfig;
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
  readonly getMenu: GetMenuHandler;
  readonly createMenu: CreateMenuHandler;
  readonly addMenuItem: AddMenuItemHandler;
  readonly updateMenuItem: UpdateMenuItemHandler;
  readonly setMenuItemAvailability: SetMenuItemAvailabilityHandler;
}

export const registerMenu = (
  container: AwilixContainer<MenuDependencies & MenuCradle>,
): void => {
  const registrations: Registrations<MenuCradle> = {
    menuOutboxEventMapper: asValue(menuEventMapper),
    menus: asFunction(
      ({ db, unitOfWork }: MenuDependencies) =>
        new DatabaseMenuRepository(createMenuCollection(db, unitOfWork)),
    ).singleton(),
    getMenu: asFunction(
      ({
        db,
        unitOfWork,
        valkey,
        valkeyConfig,
        cacheMetrics,
      }: MenuDependencies) =>
        new GetMenuHandler({
          reader: new CachedMenuReader(
            new DatabaseMenuReader(createMenuCollection(db, unitOfWork)),
            {
              client: valkey,
              ttlMs: valkeyConfig.ttlMs,
              metrics: cacheMetrics,
            },
          ),
        }),
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
