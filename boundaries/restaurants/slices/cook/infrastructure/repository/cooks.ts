import type { Db } from "mongodb";

import type { DatabaseUnitOfWork } from "../../../../pkg/infrastructure/repository/unit-of-work.ts";
import {
  VersionedCollection,
  type VersionedDocument,
} from "../../../../pkg/infrastructure/repository/versioned-collection.ts";
import {
  Cook,
  type CookRepository,
  type CookSnapshot,
} from "../../domains/cook/cook.ts";

export const COOKS_COLLECTION = "cooks";
export const COOK_SCHEMA_VERSION = 1;

/** One document per cook; it references its restaurant only by id. */
export type CookDocument = VersionedDocument & {
  restaurantId: string;
  name: string;
  onLine: boolean;
  lastAssignedAt?: Date;
  retiredAt?: Date;
};

/**
 * Runs the save of a cook with what has to commit together with it; returns
 * the committed version. DI wires the kitchen board Change Notification: the
 * board shows the Cook Pool (ADR 36).
 */
export type CookSaved = (
  write: Readonly<{
    type: string;
    id: string;
    restaurantId: string;
    save: () => Promise<number>;
  }>,
) => Promise<number>;

const toSnapshot = (document: CookDocument): CookSnapshot => ({
  id: document._id,
  restaurantId: document.restaurantId,
  name: document.name,
  onLine: document.onLine,
  lastAssignedAt: document.lastAssignedAt,
  retiredAt: document.retiredAt,
});

export class DatabaseCookRepository implements CookRepository {
  readonly #collection: VersionedCollection<CookDocument>;
  readonly #saved: CookSaved;

  constructor(db: Db, unitOfWork: DatabaseUnitOfWork, saved: CookSaved) {
    this.#collection = new VersionedCollection<CookDocument>({
      db,
      name: COOKS_COLLECTION,
      aggregateType: "Cook",
      unitOfWork,
    });
    this.#saved = saved;
  }

  async get(id: string): Promise<Cook | undefined> {
    const document = await this.#collection.findOne({ _id: id });
    return document === undefined
      ? undefined
      : Cook.restore(toSnapshot(document), document.version);
  }

  async listPool(restaurantId: string): Promise<readonly Cook[]> {
    const documents = await this.#collection.find(
      { restaurantId, retiredAt: { $exists: false } },
      { sort: { name: 1, _id: 1 } },
    );
    return documents.map((document) =>
      Cook.restore(toSnapshot(document), document.version),
    );
  }

  async save(cook: Cook): Promise<number> {
    const snapshot = cook.toSnapshot();
    return this.#saved({
      type: "Cook",
      id: cook.id,
      restaurantId: snapshot.restaurantId,
      save: () =>
        this.#collection.save(cook.id, cook.version, {
          schemaVersion: COOK_SCHEMA_VERSION,
          restaurantId: snapshot.restaurantId,
          name: snapshot.name,
          onLine: snapshot.onLine,
          lastAssignedAt: snapshot.lastAssignedAt,
          retiredAt: snapshot.retiredAt,
        }),
    });
  }
}
