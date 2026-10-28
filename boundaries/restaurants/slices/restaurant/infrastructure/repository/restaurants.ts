import type { Db } from "mongodb";

import { ConcurrencyConflictError } from "../../../../pkg/application/errors.ts";
import type { DatabaseUnitOfWork } from "../../../../pkg/infrastructure/repository/unit-of-work.ts";
import {
  VersionedCollection,
  type VersionedDocument,
} from "../../../../pkg/infrastructure/repository/versioned-collection.ts";
import type { AuthorizationRevision } from "../../applications/access-control.ts";
import type { RestaurantBatchReader } from "../../applications/batch-get-restaurants/batch-get-restaurants.ts";
import type { RestaurantPublicReader } from "../../applications/get-restaurant/get-restaurant.ts";
import type { OwnedRestaurantsReader } from "../../applications/list-owned-restaurants/list-owned-restaurants.ts";
import type { RestaurantCatalogReader } from "../../applications/list-restaurants/list-restaurants.ts";
import type {
  StoredWaitTime,
  WaitTimeStatisticsStore,
} from "../../applications/record-wait-time/record-wait-time.ts";
import type {
  CatalogRestaurantView,
  RestaurantView,
} from "../../applications/restaurant-view.ts";
import {
  Restaurant,
  type RestaurantRepository,
  type StoredRestaurantSnapshot,
} from "../../domains/restaurant/restaurant.ts";
import {
  NO_WAIT_TIME_STATISTICS,
  type WaitTimePolicy,
  type WaitTimeStatistics,
} from "../../domains/restaurant/wait-time.ts";
import type {
  RestaurantMembers,
  RestaurantMemberships,
  RevisionWrite,
} from "../reconcile/reconcile.ts";

export const RESTAURANTS_COLLECTION = "restaurants";
export const RESTAURANT_SCHEMA_VERSION = 5;

/**
 * One document per Restaurant aggregate: profile, Market (ADR 41; required
 * since schemaVersion 2, documents written before are migrated to `berlin`),
 * staff (bounded by the restaurant team) and the ZedToken of its last
 * relationship write. Menus and
 * Restaurant Orders are separate collections referencing `restaurantId`.
 * `staff` may be absent in documents written before it existed (see
 * {@link StoredRestaurantSnapshot}). `waitTime` is the Wait Time Statistics
 * read model (FR-644): absent until the first queued order, written without
 * the aggregate version and kept by its saves.
 */
export type RestaurantDocument = VersionedDocument & {
  name: string;
  ownerId: string;
  marketId: string;
  /** Since schemaVersion 4: IANA time zone (FR-201). */
  timeZone: string;
  /** Since schemaVersion 4: weekly opening hours (FR-696), empty — none. */
  openingHours: { day: number; opens: string; closes: string }[];
  isOpen: boolean;
  staff?: string[];
  authorizationRevision?: string;
  /** Since schemaVersion 3: fee of Premium Priority, absent without premium. */
  premiumFee?: { amount: string; currency: string };
  /** Since schemaVersion 5: settings of scheduled orders (FR-672), absent — none. */
  scheduledOrders?: {
    slotCapacity: number;
    openingLeadMs: number;
    closingGraceMs: number;
  };
  waitTime?: WaitTimeDocument;
};

export type WaitTimeDocument = {
  completed: { orderId: string; readyAt: Date; durationMs: number }[];
  /** Since schemaVersion 5 the moment the order joined the Kitchen Queue. */
  open: { orderId: string; queuedAt: Date }[];
  level: "NORMAL" | "ELEVATED";
  revision: number;
};

const toStatistics = (
  document: WaitTimeDocument | undefined,
): WaitTimeStatistics =>
  document === undefined
    ? NO_WAIT_TIME_STATISTICS
    : {
        completed: document.completed,
        open: document.open,
        level: document.level,
        revision: document.revision,
      };

const toWaitTimeDocument = (
  statistics: WaitTimeStatistics,
): WaitTimeDocument => ({
  completed: statistics.completed.map((entry) => ({ ...entry })),
  open: statistics.open.map((entry) => ({ ...entry })),
  level: statistics.level,
  revision: statistics.revision,
});

const toSnapshot = (
  document: RestaurantDocument,
): StoredRestaurantSnapshot => ({
  id: document._id,
  name: document.name,
  ownerId: document.ownerId,
  marketId: document.marketId,
  timeZone: document.timeZone,
  openingHours: document.openingHours,
  isOpen: document.isOpen,
  staff: document.staff,
  authorizationRevision: document.authorizationRevision,
  premiumFee: document.premiumFee,
  scheduledOrders: document.scheduledOrders,
});

const toData = (restaurant: Restaurant) => {
  const snapshot = restaurant.toSnapshot();
  return {
    schemaVersion: RESTAURANT_SCHEMA_VERSION,
    name: snapshot.name,
    ownerId: snapshot.ownerId,
    marketId: snapshot.marketId,
    timeZone: snapshot.timeZone,
    openingHours: snapshot.openingHours.map((interval) => ({ ...interval })),
    isOpen: snapshot.isOpen,
    staff: [...snapshot.staff],
    authorizationRevision: snapshot.authorizationRevision,
    premiumFee:
      snapshot.premiumFee === undefined
        ? undefined
        : { ...snapshot.premiumFee },
    scheduledOrders:
      snapshot.scheduledOrders === undefined
        ? undefined
        : { ...snapshot.scheduledOrders },
  };
};

export const createRestaurantCollection = (
  db: Db,
  unitOfWork: DatabaseUnitOfWork,
): VersionedCollection<RestaurantDocument> =>
  new VersionedCollection<RestaurantDocument>({
    db,
    name: RESTAURANTS_COLLECTION,
    aggregateType: "Restaurant",
    unitOfWork,
    readModelFields: ["waitTime"],
  });

/** Wait Time Statistics of the restaurant documents (FR-644). */
export class DatabaseRestaurantWaitTimes implements WaitTimeStatisticsStore {
  readonly #collection: VersionedCollection<RestaurantDocument>;

  constructor(collection: VersionedCollection<RestaurantDocument>) {
    this.#collection = collection;
  }

  async load(restaurantId: string): Promise<StoredWaitTime | undefined> {
    const document = await this.#collection.findOne({ _id: restaurantId });
    return document === undefined
      ? undefined
      : {
          marketId: document.marketId,
          statistics: toStatistics(document.waitTime),
        };
  }

  async save(
    restaurantId: string,
    statistics: WaitTimeStatistics,
  ): Promise<void> {
    const saved = await this.#collection.setUnversioned(restaurantId, {
      waitTime: toWaitTimeDocument(statistics),
    });
    if (!saved) {
      throw new Error(`restaurant ${restaurantId} disappeared`);
    }
  }
}

/**
 * Runs the save of a Restaurant with what has to commit together with it;
 * returns the committed version.
 */
export type RestaurantSaved = (
  write: Readonly<{
    type: string;
    id: string;
    restaurantId: string;
    save: () => Promise<number>;
  }>,
) => Promise<number>;

export class DatabaseRestaurantRepository implements RestaurantRepository {
  readonly #collection: VersionedCollection<RestaurantDocument>;
  readonly #saved: RestaurantSaved;

  /**
   * `saved` runs in the same Unit of Work after every save: the kitchen board
   * shows the restaurant, so DI wires its Change Notification (ADR 36).
   */
  constructor(
    collection: VersionedCollection<RestaurantDocument>,
    saved: RestaurantSaved,
  ) {
    this.#collection = collection;
    this.#saved = saved;
  }

  async get(id: string): Promise<Restaurant | undefined> {
    const document = await this.#collection.findOne({ _id: id });
    return document === undefined
      ? undefined
      : Restaurant.restore(toSnapshot(document), document.version);
  }

  async save(restaurant: Restaurant): Promise<number> {
    return this.#saved({
      type: "Restaurant",
      id: restaurant.id,
      restaurantId: restaurant.id,
      save: () =>
        this.#collection.save(
          restaurant.id,
          restaurant.version,
          toData(restaurant),
        ),
    });
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
export class DatabaseRestaurantMemberships implements RestaurantMemberships {
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
  marketId: document.marketId,
  timeZone: document.timeZone,
  openingHours: document.openingHours.map((interval) => ({ ...interval })),
  isOpen: document.isOpen,
  ...(document.premiumFee === undefined
    ? {}
    : { premiumFee: { ...document.premiumFee } }),
  ...(document.scheduledOrders === undefined
    ? {}
    : { scheduledOrders: { ...document.scheduledOrders } }),
});

const MINUTE_MS = 60_000;

/** Wait and baseline estimated at the time of the read (FR-204, FR-645). */
export type CatalogWaitTime = Readonly<{
  policy: WaitTimePolicy;
  now: () => Date;
}>;

const toCatalogView = (
  document: RestaurantDocument,
  waitTime: CatalogWaitTime,
): CatalogRestaurantView => {
  const statistics = toStatistics(document.waitTime);
  const { baselineMs, currentMs } = waitTime.policy.estimate(
    statistics,
    waitTime.now(),
  );
  return {
    ...toView(document),
    waitTime: {
      elevated: statistics.level === "ELEVATED",
      expectedWaitMinutes: Math.ceil(currentMs / MINUTE_MS),
      baselinePreparationMs: baselineMs,
    },
  };
};

/**
 * Query model over the restaurant documents, ordered by restaurant id. Lists
 * are filtered by Market through the `(marketId, isOpen, _id)`,
 * `(marketId, _id)` and `(ownerId, marketId, _id)` indexes.
 */
export class DatabaseRestaurantCatalog
  implements
    RestaurantPublicReader,
    RestaurantBatchReader,
    RestaurantCatalogReader,
    OwnedRestaurantsReader
{
  readonly #collection: VersionedCollection<RestaurantDocument>;
  readonly #waitTime: CatalogWaitTime;

  constructor(
    collection: VersionedCollection<RestaurantDocument>,
    waitTime: CatalogWaitTime,
  ) {
    this.#collection = collection;
    this.#waitTime = waitTime;
  }

  async get(restaurantId: string): Promise<CatalogRestaurantView | undefined> {
    const document = await this.#collection.findOne({ _id: restaurantId });
    return document === undefined
      ? undefined
      : toCatalogView(document, this.#waitTime);
  }

  async getMany(
    restaurantIds: readonly string[],
  ): Promise<readonly CatalogRestaurantView[]> {
    const documents = await this.#collection.find({
      _id: { $in: [...restaurantIds] },
    });
    return documents.map((document) => toCatalogView(document, this.#waitTime));
  }

  async list(criteria: {
    marketId: string;
    includeClosed: boolean;
    after: string | undefined;
    limit: number;
  }): Promise<readonly CatalogRestaurantView[]> {
    const documents = await this.#collection.find(
      {
        marketId: criteria.marketId,
        ...(criteria.includeClosed ? {} : { isOpen: true }),
        ...(criteria.after === undefined
          ? {}
          : { _id: { $gt: criteria.after } }),
      },
      { sort: { _id: 1 }, limit: criteria.limit },
    );
    return documents.map((document) => toCatalogView(document, this.#waitTime));
  }

  async listOwned(criteria: {
    ownerId: string;
    marketId: string;
    after: string | undefined;
    limit: number;
  }): Promise<readonly RestaurantView[]> {
    const documents = await this.#collection.find(
      {
        ownerId: criteria.ownerId,
        marketId: criteria.marketId,
        ...(criteria.after === undefined
          ? {}
          : { _id: { $gt: criteria.after } }),
      },
      { sort: { _id: 1 }, limit: criteria.limit },
    );
    return documents.map(toView);
  }
}
