import type { Db } from "mongodb";

import { migrate } from "../../pkg/infrastructure/mongodb/migrate.ts";

/**
 * migrate-mongo migrations of the `restaurants` database (local ADR 2), one
 * file each, applied in file name order: `<NNNN>-<owner>-<change>`, the owner
 * being the slice (or `pkg`) whose collection the file changes. Append only,
 * never edit an applied one: a validator or index change is a new file.
 */
export const MIGRATIONS_DIR = new URL("./migrations/", import.meta.url);

/**
 * Startup migration of every restaurants process (local ADR 2): returns the
 * file names of the applied migrations, empty when the database already is
 * current.
 */
export const migrateRestaurantsDatabase = (
  db: Db,
  log?: (message: string) => void,
): Promise<readonly string[]> => migrate(db, MIGRATIONS_DIR, log);
