import type { Db } from "mongodb";

import {
  type CollectionSpec,
  type DocumentMigration,
  migrate,
} from "../../pkg/infrastructure/mongodb/migrate.ts";
import {
  outboxCollectionSpec,
  outboxCountersCollectionSpec,
  outboxLeasesCollectionSpec,
} from "../../pkg/infrastructure/mongodb/outbox.ts";
import { restaurantOrderCollectionSpec } from "../../slices/kitchenorder/infrastructure/mongodb/restaurant-orders.ts";
import { menuCollectionSpec } from "../../slices/menu/infrastructure/mongodb/menus.ts";
import { restaurantCollectionSpec } from "../../slices/restaurant/infrastructure/mongodb/restaurants.ts";

/** Every collection of the `restaurants` database (local ADR 1). */
export const COLLECTIONS: readonly CollectionSpec[] = [
  restaurantCollectionSpec,
  menuCollectionSpec,
  restaurantOrderCollectionSpec,
  outboxCollectionSpec,
  outboxCountersCollectionSpec,
  outboxLeasesCollectionSpec,
];

/**
 * Ordered document migrations; append only, never edit an applied one. A
 * format change bumps the document `schemaVersion`, readers accept the
 * previous version until the migration has run everywhere.
 */
export const MIGRATIONS: readonly DocumentMigration[] = [];

export const migrateRestaurantsDatabase = (
  db: Db,
  log?: (message: string) => void,
): Promise<void> => migrate(db, COLLECTIONS, MIGRATIONS, log);
