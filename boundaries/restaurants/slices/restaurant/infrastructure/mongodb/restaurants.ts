import type { Db } from "mongodb";

import { ConcurrencyConflictError } from "../../../../pkg/application/errors.ts";
import type { CollectionSpec } from "../../../../pkg/infrastructure/mongodb/migrate.ts";
import type { MongoUnitOfWork } from "../../../../pkg/infrastructure/mongodb/unit-of-work.ts";
import {
  VersionedCollection,
  type VersionedDocument,
} from "../../../../pkg/infrastructure/mongodb/versioned-collection.ts";
import type { AuthorizationRevision } from "../../applications/access-control.ts";
import type { RestaurantPublicReader } from "../../applications/get-restaurant/get-restaurant.ts";
import type { RestaurantCatalogReader } from "../../applications/list-restaurants/list-restaurants.ts";
import type { RestaurantView } from "../../applications/restaurant-view.ts";
import {
  Restaurant,
  type RestaurantRepository,
  type StoredRestaurantSnapshot,
} from "../../domains/restaurant/restaurant.ts";
import type {
  RestaurantMembers,
  RestaurantMemberships,
  RevisionWrite,
} from "../reconcile/reconcile.ts";

export const RESTAURANTS_COLLECTION = "restaurants";
export const RESTAURANT_SCHEMA_VERSION = 1;

/**
 * One document per Restaurant aggregate: profile, staff (bounded by the
 * restaurant team) and the ZedToken of its last relationship write. Menus and
 * Restaurant Orders are separate collections referencing `restaurantId`.
 * `address` and `staff` may be absent in documents written before they
 * existed (see {@link StoredRestaurantSnapshot}).
 */
export type RestaurantDocument = VersionedDocument & {
  name: string;
  ownerId: string;
  isOpen: boolean;
  address?: string;
  staff?: string[];
  authorizationRevision?: string;
};

const toSnapshot = (
  document: RestaurantDocument,
): StoredRestaurantSnapshot => ({
  id: document._id,
  name: document.name,
  ownerId: document.ownerId,
  isOpen: document.isOpen,
  address: document.address,
  staff: document.staff,
  authorizationRevision: document.authorizationRevision,
});

const toData = (restaurant: Restaurant) => {
  const snapshot = restaurant.toSnapshot();
  return {
    schemaVersion: RESTAURANT_SCHEMA_VERSION,
    name: snapshot.name,
    ownerId: snapshot.ownerId,
    isOpen: snapshot.isOpen,
    address: snapshot.address,
    staff: [...snapshot.staff],
    authorizationRevision: snapshot.authorizationRevision,
  };
};

export const createRestaurantCollection = (
  db: Db,
  unitOfWork: MongoUnitOfWork,
): VersionedCollection<RestaurantDocument> =>
  new VersionedCollection<RestaurantDocument>({
    db,
    name: RESTAURANTS_COLLECTION,
    aggregateType: "Restaurant",
    unitOfWork,
  });

export class MongoRestaurantRepository implements RestaurantRepository {
  readonly #collection: VersionedCollection<RestaurantDocument>;

  constructor(collection: VersionedCollection<RestaurantDocument>) {
    this.#collection = collection;
  }

  async get(id: string): Promise<Restaurant | undefined> {
    const document = await this.#collection.findOne({ _id: id });
    return document === undefined
      ? undefined
      : Restaurant.restore(toSnapshot(document), document.version);
  }

  async save(restaurant: Restaurant): Promise<number> {
    return this.#collection.save(
      restaurant.id,
      restaurant.version,
      toData(restaurant),
    );
  }

  /** Revision reader of the SpiceDB adapter (stored ZedToken, FR-447). */
  async authorizationRevision(
    restaurantId: string,
  ): Promise<AuthorizationRevision | undefined> {
    const document = await this.#collection.collection.findOne(
      { _id: restaurantId },
      { projection: { authorizationRevision: 1 } },
    );
    return document?.authorizationRevision ?? undefined;
  }
}

const toMembers = (document: RestaurantDocument): RestaurantMembers => ({
  restaurantId: document._id,
  version: Number(document.version),
  ownerId: document.ownerId,
  staff: document.staff ?? [],
});

/** Business side of the relationship reconciliation (FR-445). */
export class MongoRestaurantMemberships implements RestaurantMemberships {
  readonly #collection: VersionedCollection<RestaurantDocument>;

  constructor(collection: VersionedCollection<RestaurantDocument>) {
    this.#collection = collection;
  }

  async membersOf(
    ids: readonly string[],
  ): Promise<ReadonlyMap<string, RestaurantMembers>> {
    const documents = await this.#collection.find({
      _id: { $in: [...new Set(ids)] },
    });
    return new Map(
      documents.map((document) => [document._id, toMembers(document)]),
    );
  }

  async page(
    after: string | undefined,
    limit: number,
  ): Promise<readonly RestaurantMembers[]> {
    const documents = await this.#collection.find(
      after === undefined ? {} : { _id: { $gt: after } },
      { sort: { _id: 1 }, limit },
    );
    return documents.map(toMembers);
  }

  /**
   * Stores the revision of a repairing TOUCH. A concurrent save is retried on
   * the fresh version; a deleted restaurant is skipped.
   */
  async recordAuthorizationRevision(
    restaurantId: string,
    revision: AuthorizationRevision,
  ): Promise<RevisionWrite | undefined> {
    for (let attempt = 0; ; attempt += 1) {
      const document = await this.#collection.findOne({ _id: restaurantId });
      if (document === undefined) return undefined;
      const restaurant = Restaurant.restore(
        toSnapshot(document),
        document.version,
      );
      restaurant.recordAuthorizationRevision(revision);
      try {
        const to = await this.#collection.save(
          restaurantId,
          document.version,
          toData(restaurant),
        );
        return { from: Number(document.version), to };
      } catch (error) {
        if (!(error instanceof ConcurrencyConflictError) || attempt >= 2) {
          throw error;
        }
      }
    }
  }
}

const toView = (document: RestaurantDocument): RestaurantView => ({
  restaurantId: document._id,
  name: document.name,
  isOpen: document.isOpen,
  address: document.address ?? "",
});

/** Query model over the restaurant documents, ordered by restaurant id. */
export class MongoRestaurantCatalog
  implements RestaurantPublicReader, RestaurantCatalogReader
{
  readonly #collection: VersionedCollection<RestaurantDocument>;

  constructor(collection: VersionedCollection<RestaurantDocument>) {
    this.#collection = collection;
  }

  async get(restaurantId: string): Promise<RestaurantView | undefined> {
    const document = await this.#collection.findOne({ _id: restaurantId });
    return document === undefined ? undefined : toView(document);
  }

  async list(criteria: {
    includeClosed: boolean;
    after: string | undefined;
    limit: number;
  }): Promise<readonly RestaurantView[]> {
    const documents = await this.#collection.find(
      {
        ...(criteria.includeClosed ? {} : { isOpen: true }),
        ...(criteria.after === undefined
          ? {}
          : { _id: { $gt: criteria.after } }),
      },
      { sort: { _id: 1 }, limit: criteria.limit },
    );
    return documents.map(toView);
  }
}

export const restaurantCollectionSpec: CollectionSpec = {
  name: RESTAURANTS_COLLECTION,
  validator: {
    $jsonSchema: {
      bsonType: "object",
      required: [
        "_id",
        "version",
        "schemaVersion",
        "name",
        "ownerId",
        "isOpen",
      ],
      properties: {
        _id: { bsonType: "string", minLength: 1 },
        version: { bsonType: ["int", "long"], minimum: 1 },
        schemaVersion: { bsonType: "int", minimum: 1 },
        name: { bsonType: "string", minLength: 1 },
        ownerId: { bsonType: "string", minLength: 1 },
        isOpen: { bsonType: "bool" },
        address: { bsonType: "string" },
        staff: {
          bsonType: "array",
          uniqueItems: true,
          items: { bsonType: "string", minLength: 1 },
        },
        authorizationRevision: { bsonType: "string", minLength: 1 },
      },
    },
  },
  indexes: [
    // Public catalog: open restaurants paged by id.
    { key: { isOpen: 1, _id: 1 }, name: "restaurants_open_by_id" },
    { key: { ownerId: 1 }, name: "restaurants_owner" },
  ],
};
