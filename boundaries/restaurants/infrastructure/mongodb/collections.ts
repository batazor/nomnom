import type { Db } from "mongodb";

import {
  type CollectionSpec,
  type DocumentMigration,
  migrate,
} from "../../pkg/infrastructure/mongodb/migrate.ts";
import { inboxCollectionSpec } from "../../pkg/infrastructure/mongodb/inbox.ts";
import {
  dropOutboxRelayMigration,
  outboxCollectionSpec,
} from "../../pkg/infrastructure/mongodb/outbox.ts";
import { restaurantOrderCollectionSpec } from "../../slices/restaurantorder/infrastructure/mongodb/restaurant-orders.ts";
import { menuCollectionSpec } from "../../slices/menu/infrastructure/mongodb/menus.ts";
import { restaurantCollectionSpec } from "../../slices/restaurant/infrastructure/mongodb/restaurants.ts";
import { simulatorCollectionSpec } from "../../slices/simulator/infrastructure/mongodb/simulator-state.ts";

/** Every collection of the `restaurants` database (local ADR 1). */
export const COLLECTIONS: readonly CollectionSpec[] = [
  restaurantCollectionSpec,
  menuCollectionSpec,
  restaurantOrderCollectionSpec,
  simulatorCollectionSpec,
  outboxCollectionSpec,
  inboxCollectionSpec,
];

/**
 * Ordered document migrations; append only, never edit an applied one. A
 * format change bumps the document `schemaVersion`, readers accept the
 * previous version until the migration has run everywhere.
 */
export const MIGRATIONS: readonly DocumentMigration[] = [
  dropOutboxRelayMigration,
];

/**
 * Startup migration of every restaurants process (local ADR 2): returns the
 * applied changes, empty when the database already is current.
 */
export const migrateRestaurantsDatabase = (
  db: Db,
  log?: (message: string) => void,
): Promise<readonly string[]> => migrate(db, COLLECTIONS, MIGRATIONS, log);
