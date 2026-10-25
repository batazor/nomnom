// Composition root of `cmd/restaurants-migrate` (ADR 5): the one-shot
// migration of the restaurants database. The MongoDB connection is acquired
// and released by cmd/restaurants-migrate.
import { type AwilixContainer, asFunction, asValue } from "awilix";
import type { Db } from "mongodb";

import { migrateRestaurantsDatabase } from "../infrastructure/mongodb/collections.ts";
import {
  createProcessContainer,
  type Registrations,
} from "../pkg/di/container.ts";

export type MigrateResources = Readonly<{
  db: Db;
  log: (message: string) => void;
}>;

/** Collections, validators, indexes and pending document migrations. */
export interface Migration {
  run(): Promise<void>;
}

export interface MigrateCradle {
  readonly db: Db;
  readonly log: (message: string) => void;
  readonly migration: Migration;
}

export const createMigrateContainer = (
  resources: MigrateResources,
): AwilixContainer<MigrateCradle> => {
  const container = createProcessContainer<MigrateCradle>();
  const registrations: Registrations<MigrateCradle> = {
    db: asValue(resources.db),
    log: asValue(resources.log),
    migration: asFunction(
      ({ db, log }: MigrateCradle): Migration => ({
        run: () => migrateRestaurantsDatabase(db, log),
      }),
    ).singleton(),
  };
  container.register(registrations);
  return container;
};
