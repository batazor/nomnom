import { randomUUID } from "node:crypto";
import { setTimeout as delay } from "node:timers/promises";

import {
  BSON,
  type CreateIndexesOptions,
  type Db,
  type Document,
  type IndexSpecification,
} from "mongodb";

/** Collection with its validator and indexes, owned by one slice. */
export type CollectionSpec = Readonly<{
  name: string;
  /** `$jsonSchema` validator; documents violating it are rejected. */
  validator: Document;
  indexes: readonly (Readonly<{ key: IndexSpecification }> &
    CreateIndexesOptions & { name: string })[];
}>;

/**
 * Data migration of already stored documents, applied once and recorded in
 * `schema_migrations`. Must be idempotent: a crash before the record is
 * written runs it again.
 */
export type DocumentMigration = Readonly<{
  id: string;
  up(db: Db): Promise<void>;
}>;

export const MIGRATIONS_COLLECTION = "schema_migrations";
/** Lease of the process that is migrating the database right now. */
export const MIGRATION_LOCK_COLLECTION = "schema_migrations_lock";

const LOCK_ID = "migrate";
/** A crashed holder blocks the others at most this long. */
const LEASE_MS = 30_000;
const RENEW_INTERVAL_MS = 10_000;
const ACQUIRE_POLL_MS = 500;

/**
 * Brings the database to the current schema and returns what changed (empty
 * when it already was current): creates missing collections with validators,
 * replaces changed validators (`collMod`), creates missing indexes and
 * applies pending document migrations in order. Every process of restaurants
 * runs it at startup before serving.
 *
 * The run holds a lease in `schema_migrations_lock`: API and consumer pods
 * start together, and while the DDL steps tolerate a concurrent run, document
 * migrations do not (two `dropIndex` of the same index fail on the loser). The
 * lease expires after `LEASE_MS` without renewal, so a crashed holder blocks
 * the others only that long; the next holder finds nothing to change.
 */
export async function migrate(
  db: Db,
  specs: readonly CollectionSpec[],
  migrations: readonly DocumentMigration[] = [],
  log: (message: string) => void = () => {},
): Promise<readonly string[]> {
  const release = await acquireLease(db, log);
  try {
    const changes = [
      ...(await ensureCollections(db, specs)),
      ...(await applyMigrations(db, migrations)),
    ];
    for (const change of changes) log(change);
    log(
      changes.length === 0
        ? "database schema is up to date, nothing to migrate"
        : `database schema migrated: ${changes.length} changes`,
    );
    return changes;
  } finally {
    await release();
  }
}

async function ensureCollections(
  db: Db,
  specs: readonly CollectionSpec[],
): Promise<string[]> {
  const changes: string[] = [];
  const existing = new Map(
    (await db.listCollections().toArray()).map((collection) => [
      collection.name,
      "options" in collection ? collection.options : undefined,
    ]),
  );
  for (const spec of specs) {
    const options = existing.get(spec.name);
    if (options === undefined) {
      try {
        await db.createCollection(spec.name, {
          validator: spec.validator,
          validationLevel: "strict",
          validationAction: "error",
        });
      } catch (error) {
        // NamespaceExists: created after listCollections (an expired lease).
        if (!(isMongoError(error) && error.code === 48)) throw error;
        await db.command({ collMod: spec.name, validator: spec.validator });
      }
      changes.push(`created collection ${spec.name}`);
    } else if (!hasValidator(options, spec.validator)) {
      await db.command({
        collMod: spec.name,
        validator: spec.validator,
        validationLevel: "strict",
        validationAction: "error",
      });
      changes.push(`updated validator of ${spec.name}`);
    }
    if (spec.indexes.length === 0) continue;
    const collection = db.collection(spec.name);
    const indexes = new Set(
      (await collection.listIndexes().toArray()).map((index) => index.name),
    );
    // All specs, not only the missing ones: an existing index with the same
    // name and other options fails with IndexOptionsConflict instead of
    // being silently kept.
    await collection.createIndexes(
      spec.indexes.map(({ key, ...options }) => ({ key, ...options })) as {
        key: Record<string, 1 | -1>;
      }[],
    );
    for (const index of spec.indexes) {
      if (!indexes.has(index.name)) {
        changes.push(`created index ${spec.name}.${index.name}`);
      }
    }
  }
  return changes;
}

async function applyMigrations(
  db: Db,
  migrations: readonly DocumentMigration[],
): Promise<string[]> {
  const changes: string[] = [];
  const applied = db.collection<{ _id: string; appliedAt: Date }>(
    MIGRATIONS_COLLECTION,
  );
  for (const migration of migrations) {
    if ((await applied.findOne({ _id: migration.id })) !== null) continue;
    await migration.up(db);
    await applied.updateOne(
      { _id: migration.id },
      { $setOnInsert: { appliedAt: new Date() } },
      { upsert: true },
    );
    changes.push(`applied migration ${migration.id}`);
  }
  return changes;
}

const hasValidator = (options: Document | undefined, validator: Document) =>
  options?.validationLevel === "strict" &&
  options.validationAction === "error" &&
  BSON.EJSON.stringify(options.validator) === BSON.EJSON.stringify(validator);

type MigrationLock = Readonly<{
  _id: string;
  owner: string;
  expiresAt: Date;
}>;

/**
 * Takes the lease, waiting while another live process holds it, and renews
 * it until the returned release. Expiry is computed by the server (`$$NOW`),
 * so pod clock skew does not matter.
 */
async function acquireLease(
  db: Db,
  log: (message: string) => void,
): Promise<() => Promise<void>> {
  const locks = db.collection<MigrationLock>(MIGRATION_LOCK_COLLECTION);
  const owner = randomUUID();
  const extend = [
    { $set: { owner, expiresAt: { $add: ["$$NOW", LEASE_MS] } } },
  ];
  for (let waiting = false; ; waiting = true) {
    try {
      // Matches a missing (upsert) or expired lease; a live one of another
      // process makes the upsert collide on `_id`. An upsert filter cannot
      // use `$$NOW`, so the server time comes from `hello`.
      const { localTime } = await db.command({ hello: 1 });
      await locks.updateOne(
        { _id: LOCK_ID, expiresAt: { $lt: localTime as Date } },
        extend,
        { upsert: true },
      );
      break;
    } catch (error) {
      if (!(isMongoError(error) && error.code === 11000)) throw error;
    }
    if (!waiting) log("waiting for another process to finish migrating");
    await delay(ACQUIRE_POLL_MS);
  }
  // A failed renewal is retried on the next tick; if the lease expires
  // meanwhile, the next holder repeats only idempotent steps.
  const renewal = setInterval(() => {
    locks.updateOne({ _id: LOCK_ID, owner }, extend).catch(() => {});
  }, RENEW_INTERVAL_MS);
  renewal.unref();
  return async () => {
    clearInterval(renewal);
    await locks.deleteOne({ _id: LOCK_ID, owner });
  };
}

const isMongoError = (error: unknown): error is { code: unknown } =>
  typeof error === "object" && error !== null && "code" in error;
