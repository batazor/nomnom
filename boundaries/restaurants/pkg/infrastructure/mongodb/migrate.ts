import { extname } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";

import { config, up } from "migrate-mongo";
import type { Db, Document, IndexDescription } from "mongodb";

/** Applied migrations of migrate-mongo: one `{ fileName, appliedAt }` each. */
export const MIGRATIONS_CHANGELOG_COLLECTION = "migrations_changelog";
/** migrate-mongo lock: at most one document while a process migrates. */
export const MIGRATIONS_LOCK_COLLECTION = "migrations_lock";

/**
 * TTL of the lock document: a crashed holder blocks the others until the
 * MongoDB TTL monitor (every 60 s) removes it. The lock is not renewed, so a
 * run longer than this lets another process in; migrations are idempotent.
 */
const LOCK_TTL_SECONDS = 30;
const LOCK_POLL_MS = 500;
/** `.ts` under Node type stripping, `.js` in `dist`. */
const MIGRATION_FILE_EXTENSION = extname(import.meta.filename);

/** Collection with its `$jsonSchema` validator and indexes, as of one migration. */
export type CollectionSchema = Readonly<{
  name: string;
  validator: Document;
  indexes: readonly (IndexDescription & { name: string })[];
}>;

/**
 * Migration step: creates the collection with its validator or replaces the
 * validator of an existing one (`collMod`), then creates the indexes.
 * Idempotent, also on a database created before migrate-mongo: an existing
 * index with the same name and other options fails with IndexOptionsConflict
 * instead of being silently kept.
 */
export async function ensureCollection(
  db: Db,
  schema: CollectionSchema,
): Promise<void> {
  const validation = {
    validator: schema.validator,
    validationLevel: "strict",
    validationAction: "error",
  } as const;
  const exists =
    (
      await db
        .listCollections({ name: schema.name }, { nameOnly: true })
        .toArray()
    ).length > 0;
  if (exists) {
    await db.command({ collMod: schema.name, ...validation });
  } else {
    try {
      await db.createCollection(schema.name, validation);
    } catch (error) {
      // NamespaceExists: created by a concurrent run after listCollections.
      if (!(isMongoError(error) && error.code === 48)) throw error;
      await db.command({ collMod: schema.name, ...validation });
    }
  }
  if (schema.indexes.length > 0) {
    await db.collection(schema.name).createIndexes([...schema.indexes]);
  }
}

/**
 * Applies the pending migrate-mongo migrations of `migrationsDir` in file
 * name order with the client of the process and returns their file names
 * (empty when the database already is current). Every process of restaurants
 * runs it at startup before serving; a failed migration rejects.
 *
 * migrate-mongo's lock is a document in `migrations_lock`, but `up` rejects
 * instead of waiting when it is taken and checks and inserts it in two steps.
 * A unique index on a field the lock documents never have makes the insert
 * atomic (one missing value per unique index), and a taken lock is retried
 * until the holder clears it or its TTL expires. `up` reads the changelog
 * before taking the lock, so a process that read it just before the holder
 * finished repeats the holder's migrations: they must be idempotent.
 */
export async function migrate(
  db: Db,
  migrationsDir: URL,
  log: (message: string) => void = () => {},
): Promise<readonly string[]> {
  config.set({
    migrationsDir: fileURLToPath(migrationsDir),
    migrationFileExtension: MIGRATION_FILE_EXTENSION,
    changelogCollectionName: MIGRATIONS_CHANGELOG_COLLECTION,
    lockCollectionName: MIGRATIONS_LOCK_COLLECTION,
    lockTtl: LOCK_TTL_SECONDS,
    useFileHash: false,
  });
  // The same TTL index migrate-mongo creates (without awaiting) on every
  // lock access, plus the one that makes the lock exclusive.
  await db.collection(MIGRATIONS_LOCK_COLLECTION).createIndexes([
    {
      key: { createdAt: 1 },
      name: "createdAt_1",
      expireAfterSeconds: LOCK_TTL_SECONDS,
    },
    { key: { exclusive: 1 }, name: "migrations_lock_exclusive", unique: true },
  ]);
  for (let waiting = false; ; waiting = true) {
    try {
      const migrated = await up(db, db.client);
      for (const fileName of migrated) log(`applied migration ${fileName}`);
      if (migrated.length === 0) {
        log("database schema is up to date, nothing to migrate");
      }
      return migrated;
    } catch (error) {
      if (!isLockTaken(error)) throw error;
    }
    if (!waiting) log("waiting for another process to finish migrating");
    await delay(LOCK_POLL_MS);
  }
}

/** migrate-mongo reports a taken lock only by message. */
const isLockTaken = (error: unknown) =>
  error instanceof Error &&
  (error.message === "Could not migrate up, a lock is in place." ||
    (error.message.startsWith("Could not create a lock:") &&
      error.message.includes("E11000")));

const isMongoError = (error: unknown): error is { code: unknown } =>
  typeof error === "object" && error !== null && "code" in error;
