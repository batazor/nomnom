// Awilix registrations of the `menu` slice (ADR 5): MongoDB adapters of its
// ports. Handlers of menu use cases are not exposed through transport yet.
import { type AwilixContainer, asFunction } from "awilix";
import type { Db } from "mongodb";

import type { Registrations } from "../../../pkg/di/container.ts";
import type { MongoUnitOfWork } from "../../../pkg/infrastructure/mongodb/unit-of-work.ts";
import type { MenuRepository } from "../domains/menu/menu.ts";
import {
  createMenuCollection,
  MongoMenuRepository,
} from "../infrastructure/mongodb/menus.ts";

/** Application-scope dependencies the slice takes from the process. */
export interface MenuDependencies {
  readonly db: Db;
  readonly unitOfWork: MongoUnitOfWork;
}

/** What the slice provides to the process and to other slices. */
export interface MenuCradle {
  readonly menus: MenuRepository;
}

export const registerMenu = (
  container: AwilixContainer<MenuDependencies & MenuCradle>,
): void => {
  const registrations: Registrations<MenuCradle> = {
    menus: asFunction(
      ({ db, unitOfWork }: MenuDependencies) =>
        new MongoMenuRepository(createMenuCollection(db, unitOfWork)),
    ).singleton(),
  };
  container.register(registrations);
};
