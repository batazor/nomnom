// Awilix registrations of the `menu` slice (ADR 5): MongoDB adapters of its
// ports, the Outbox mapper of the Menu aggregate and the public Get Menu query
// served by gRPC.
import { type AwilixContainer, asFunction, asValue } from "awilix";
import type { Db } from "mongodb";

import type { Registrations } from "../../../pkg/di/container.ts";
import type { OutboxEventMapper } from "../../../pkg/infrastructure/mongodb/outbox.ts";
import type { MongoUnitOfWork } from "../../../pkg/infrastructure/mongodb/unit-of-work.ts";
import { GetMenuHandler } from "../applications/get-menu/get-menu.ts";
import type { MenuRepository } from "../domains/menu/menu.ts";
import { menuEventMapper } from "../infrastructure/integration/menu-events.ts";
import {
  createMenuCollection,
  MongoMenuReader,
  MongoMenuRepository,
} from "../infrastructure/mongodb/menus.ts";

/** Application-scope dependencies the slice takes from the process. */
export interface MenuDependencies {
  readonly db: Db;
  readonly unitOfWork: MongoUnitOfWork;
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
}

export const registerMenu = (
  container: AwilixContainer<MenuDependencies & MenuCradle>,
): void => {
  const registrations: Registrations<MenuCradle> = {
    menuOutboxEventMapper: asValue(menuEventMapper),
    menus: asFunction(
      ({ db, unitOfWork }: MenuDependencies) =>
        new MongoMenuRepository(createMenuCollection(db, unitOfWork)),
    ).singleton(),
    getMenu: asFunction(
      ({ db, unitOfWork }: MenuDependencies) =>
        new GetMenuHandler({
          reader: new MongoMenuReader(createMenuCollection(db, unitOfWork)),
        }),
    ).singleton(),
  };
  container.register(registrations);
};
