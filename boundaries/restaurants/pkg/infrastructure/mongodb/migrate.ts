import type {
  CreateIndexesOptions,
  Db,
  Document,
  IndexSpecification,
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

/**
 * Idempotent ensure step: creates missing collections with validators,
 * replaces validators of existing ones (`collMod`), creates missing indexes
 * and applies pending document migrations in order. Concurrent runs are safe:
 * collection and index creation are idempotent and a migration id is
 * recorded under a unique `_id`.
 */
export async function migrate(
  db: Db,
  specs: readonly CollectionSpec[],
  migrations: readonly DocumentMigration[] = [],
  log: (message: string) => void = () => {},
): Promise<void> {
  const existing = new Set(
    (await db.listCollections({}, { nameOnly: true }).toArray()).map(
      (collection) => collection.name,
    ),
  );
  for (const spec of specs) {
    if (existing.has(spec.name)) {
      await db.command({
        collMod: spec.name,
        validator: spec.validator,
        validationLevel: "strict",
        validationAction: "error",
      });
    } else {
      try {
        await db.createCollection(spec.name, {
          validator: spec.validator,
          validationLevel: "strict",
          validationAction: "error",
        });
      } catch (error) {
        // NamespaceExists: a concurrent run created it first.
        if (!(isMongoError(error) && error.code === 48)) throw error;
        await db.command({ collMod: spec.name, validator: spec.validator });
      }
      log(`created collection ${spec.name}`);
    }
    if (spec.indexes.length > 0) {
      await db.collection(spec.name).createIndexes(
        spec.indexes.map(({ key, ...options }) => ({ key, ...options })) as {
          key: Record<string, 1 | -1>;
        }[],
      );
    }
  }

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
    log(`applied migration ${migration.id}`);
  }
}

const isMongoError = (error: unknown): error is { code: unknown } =>
  typeof error === "object" && error !== null && "code" in error;
