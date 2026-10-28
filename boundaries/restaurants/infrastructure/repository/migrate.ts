import type { Db } from "mongodb";

import {
  type MigrationSet,
  migrate,
} from "../../pkg/infrastructure/repository/migrate.ts";

/**
 * migrate-mongo migration sets of the `restaurants` database (local ADR 2),
 * applied in this order: `pkg` (shared collections and service-wide
 * cleanups) first, then every slice its own collections. Each set is a
 * directory of `<NNNN>-<change>` files numbered from 0001 with its own
 * changelog `migrations_changelog_<id>`. The `id` is a stable storage id
 * named after the data of the set, part of the schema contract: it never
 * follows directory or slice renames, and changing it requires a migration.
 * Append only, never edit an applied file: a validator or index
 * change is a new file of the owning set; a new slice with collections adds
 * its set here.
 */
export const RESTAURANTS_MIGRATION_SETS: readonly MigrationSet[] = [
  {
    id: "messaging",
    dir: new URL("../../pkg/infrastructure/repository/migrations/", import.meta.url),
  },
  {
    id: "restaurants",
    dir: new URL(
      "../../slices/restaurant/infrastructure/repository/migrations/",
      import.meta.url,
    ),
  },
  {
    id: "menus",
    dir: new URL(
      "../../slices/menu/infrastructure/repository/migrations/",
      import.meta.url,
    ),
  },
  {
    id: "restaurant_orders",
    dir: new URL(
      "../../slices/restaurantorder/infrastructure/repository/migrations/",
      import.meta.url,
    ),
  },
  {
    id: "cooks",
    dir: new URL(
      "../../slices/cook/infrastructure/repository/migrations/",
      import.meta.url,
    ),
  },
  {
    id: "schedule_slots",
    dir: new URL(
      "../../slices/scheduleslot/infrastructure/repository/migrations/",
      import.meta.url,
    ),
  },
  {
    id: "simulator",
    dir: new URL(
      "../../slices/simulator/infrastructure/repository/migrations/",
      import.meta.url,
    ),
  },
];

/**
 * Startup migration of every restaurants process (local ADR 2): returns the
 * applied migrations as `<id>/<fileName>`, empty when the database already
 * is current.
 */
export const migrateRestaurantsDatabase = (
  db: Db,
  log?: (message: string) => void,
): Promise<readonly string[]> =>
  migrate(db, RESTAURANTS_MIGRATION_SETS, log);
