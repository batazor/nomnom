import type { Db } from "mongodb";

/**
 * Removes what the previous migrators left behind: the applied-migrations
 * and lease collections of the home-grown migrator that migrate-mongo
 * replaced (ADR 39) and the single changelog of migrate-mongo that the
 * per-set changelogs replaced (local ADR 2). Idempotent: a missing
 * collection is skipped.
 */
export async function up(db: Db): Promise<void> {
  const collections = new Set(
    (await db.listCollections({}, { nameOnly: true }).toArray()).map(
      (collection) => collection.name,
    ),
  );
  for (const name of [
    "schema_migrations",
    "schema_migrations_lock",
    "migrations_changelog",
  ]) {
    if (collections.has(name)) await db.dropCollection(name);
  }
}
