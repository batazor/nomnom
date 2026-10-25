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
import type { Logger } from "../pkg/telemetry/logger.ts";

export type MigrateResources = Readonly<{
  db: Db;
  logger: Logger;
}>;

/** Collections, validators, indexes and pending document migrations. */
export interface Migration {
  run(): Promise<void>;
}

export interface MigrateCradle {
  readonly db: Db;
  readonly logger: Logger;
  readonly migration: Migration;
}

export const createMigrateContainer = (
  resources: MigrateResources,
): AwilixContainer<MigrateCradle> => {
  const container = createProcessContainer<MigrateCradle>();
  const registrations: Registrations<MigrateCradle> = {
    db: asValue(resources.db),
    logger: asValue(resources.logger),
    migration: asFunction(
      ({ db, logger }: MigrateCradle): Migration => ({
        run: () =>
          migrateRestaurantsDatabase(
            db,
            logger.child({ operation: "migrate" }).info,
          ),
      }),
    ).singleton(),
  };
  container.register(registrations);
  return container;
};
