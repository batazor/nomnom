import type { Db } from "mongodb";

/**
 * Removes the applied-migrations and lease collections of the migrator that
 * migrate-mongo replaced (ADR 39). Idempotent: a missing collection is skipped.
 */
export async function up(db: Db): Promise<void> {
  const collections = new Set(
    (await db.listCollections({}, { nameOnly: true }).toArray()).map(
      (collection) => collection.name,
    ),
  );
  for (const name of ["schema_migrations", "schema_migrations_lock"]) {
    if (collections.has(name)) await db.dropCollection(name);
  }
}
