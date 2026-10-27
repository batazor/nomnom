import type { Collection, Db, Document, Filter, FindOptions } from "mongodb";
import { MongoServerError } from "mongodb";

import { ConcurrencyConflictError } from "../../application/errors.ts";
import type { DatabaseUnitOfWork } from "./unit-of-work.ts";

/** Technical fields every aggregate document carries next to its data. */
export type VersionedDocument = Document & {
  _id: string;
  /** Optimistic concurrency: committed aggregate version, starts at 1. */
  version: number;
  /** Document format version for managed schema evolution. */
  schemaVersion: number;
};

/** Duplicate key error of a unique index other than `_id`. */
export type DuplicateKeyMapper<D> = (
  keyPattern: Record<string, unknown>,
  document: D,
) => Error | undefined;

/**
 * Aggregate collection with optimistic concurrency on `version`. Every
 * operation joins the transaction of the active {@link DatabaseUnitOfWork};
 * outside it, single-document writes are atomic on their own.
 */
export class VersionedCollection<D extends VersionedDocument> {
  readonly collection: Collection<D>;
  readonly #aggregateType: string;
  readonly #unitOfWork: DatabaseUnitOfWork;
  readonly #duplicateKey: DuplicateKeyMapper<D>;
  readonly #readModelFields: readonly string[];

  constructor(options: {
    db: Db;
    name: string;
    aggregateType: string;
    unitOfWork: DatabaseUnitOfWork;
    duplicateKey?: DuplicateKeyMapper<D>;
    /**
     * Fields of a read model kept in the aggregate document: {@link save}
     * leaves them as stored, {@link setUnversioned} writes them.
     */
    readModelFields?: readonly string[];
  }) {
    this.collection = options.db.collection<D>(options.name);
    this.#aggregateType = options.aggregateType;
    this.#unitOfWork = options.unitOfWork;
    this.#duplicateKey = options.duplicateKey ?? (() => undefined);
    this.#readModelFields = options.readModelFields ?? [];
  }

  get #session() {
    return this.#unitOfWork.session;
  }

  async findOne(filter: Filter<D>): Promise<D | undefined> {
    const found = await this.collection.findOne(filter, {
      session: this.#session,
    });
    return (found ?? undefined) as D | undefined;
  }

  async find(filter: Filter<D>, options: FindOptions = {}): Promise<D[]> {
    return (await this.collection
      .find(filter, { ...options, session: this.#session })
      .toArray()) as D[];
  }

  async count(filter: Filter<D>): Promise<number> {
    return this.collection.countDocuments(filter, { session: this.#session });
  }

  /**
   * Inserts the document for `expectedVersion` 0, otherwise replaces it only
   * if the stored version still equals `expectedVersion`. Returns the new
   * version; a lost race is {@link ConcurrencyConflictError}.
   */
  async save(
    id: string,
    expectedVersion: number,
    data: Omit<D, "_id" | "version">,
  ): Promise<number> {
    const version = expectedVersion + 1;
    const document = { ...data, _id: id, version } as unknown as D;
    try {
      if (expectedVersion === 0) {
        await this.collection.insertOne(document as never, {
          session: this.#session,
        });
      } else {
        const filter = { _id: id, version: expectedVersion } as Filter<D>;
        // Read model fields survive the replacement; `$literal` keeps
        // values that start with `$` from being read as field paths.
        const result =
          this.#readModelFields.length === 0
            ? await this.collection.replaceOne(filter, document, {
                session: this.#session,
              })
            : await this.collection.updateOne(
                filter,
                [
                  {
                    $replaceWith: {
                      $mergeObjects: [
                        { $literal: document },
                        Object.fromEntries(
                          this.#readModelFields.map((field) => [
                            field,
                            `$${field}`,
                          ]),
                        ),
                      ],
                    },
                  },
                ],
                { session: this.#session },
              );
        if (result.matchedCount === 0) {
          throw new ConcurrencyConflictError(this.#aggregateType, id);
        }
      }
    } catch (error) {
      if (error instanceof MongoServerError && error.code === 11000) {
        const keyPattern = (error.keyPattern ?? {}) as Record<string, unknown>;
        if ("_id" in keyPattern) {
          throw new ConcurrencyConflictError(this.#aggregateType, id);
        }
        throw this.#duplicateKey(keyPattern, document) ?? error;
      }
      throw error;
    }
    return version;
  }

  /**
   * Writes read model fields of an existing document without checking or
   * changing `version`; `false` when the document does not exist.
   */
  async setUnversioned(id: string, fields: Partial<D>): Promise<boolean> {
    const unknown = Object.keys(fields).filter(
      (field) => !this.#readModelFields.includes(field),
    );
    if (unknown.length > 0) {
      throw new Error(`not read model fields: ${unknown.join(", ")}`);
    }
    const result = await this.collection.updateOne(
      { _id: id } as Filter<D>,
      { $set: fields } as never,
      { session: this.#session },
    );
    return result.matchedCount > 0;
  }
}
