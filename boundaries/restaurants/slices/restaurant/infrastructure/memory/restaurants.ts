import type { Subject } from "../../../../pkg/application/subject.ts";
import { InMemoryDocumentStore } from "../../../../pkg/infrastructure/memory/document-store.ts";
import type {
  OwnerRelationshipWriter,
  RestaurantAccessControl,
  StaffRelationshipWriter,
} from "../../applications/access-control.ts";
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
} from "../reconcile/reconcile.ts";

export const createRestaurantStore =
  (): InMemoryDocumentStore<StoredRestaurantSnapshot> =>
    new InMemoryDocumentStore<StoredRestaurantSnapshot>("Restaurant");

export class InMemoryRestaurantRepository implements RestaurantRepository {
  readonly #store: InMemoryDocumentStore<StoredRestaurantSnapshot>;

  constructor(store: InMemoryDocumentStore<StoredRestaurantSnapshot>) {
    this.#store = store;
  }

  async get(id: string): Promise<Restaurant | undefined> {
    const stored = this.#store.get(id);
    return stored === undefined
      ? undefined
      : Restaurant.restore(stored.document, stored.version);
  }

  async save(restaurant: Restaurant): Promise<number> {
    return this.#store.save(
      restaurant.id,
      restaurant.version,
      restaurant.toSnapshot(),
    );
  }
}

/**
 * Business side of the relationship reconciliation over the same in-memory
 * documents: owner and staff of every stored restaurant.
 */
export class InMemoryRestaurantMemberships implements RestaurantMemberships {
  readonly #store: InMemoryDocumentStore<StoredRestaurantSnapshot>;

  constructor(store: InMemoryDocumentStore<StoredRestaurantSnapshot>) {
    this.#store = store;
  }

  async membersOf(
    ids: readonly string[],
  ): Promise<ReadonlyMap<string, RestaurantMembers>> {
    const members = new Map<string, RestaurantMembers>();
    for (const id of new Set(ids)) {
      const stored = this.#store.get(id);
      if (stored !== undefined) members.set(id, toMembers(stored.document));
    }
    return members;
  }

  async page(
    after: string | undefined,
    limit: number,
  ): Promise<readonly RestaurantMembers[]> {
    return this.#store
      .all()
      .map((stored) => stored.document)
      .filter((restaurant) => after === undefined || restaurant.id > after)
      .sort((left, right) => (left.id < right.id ? -1 : 1))
      .slice(0, limit)
      .map(toMembers);
  }
}

const toMembers = (snapshot: StoredRestaurantSnapshot): RestaurantMembers => ({
  restaurantId: snapshot.id,
  ownerId: snapshot.ownerId,
  staff: snapshot.staff ?? [],
});

const toView = (snapshot: StoredRestaurantSnapshot): RestaurantView => ({
  restaurantId: snapshot.id,
  name: snapshot.name,
  isOpen: snapshot.isOpen,
  address: snapshot.address ?? "",
});

/** Query model over the same in-memory documents, ordered by restaurant id. */
export class InMemoryRestaurantCatalog
  implements RestaurantPublicReader, RestaurantCatalogReader
{
  readonly #store: InMemoryDocumentStore<StoredRestaurantSnapshot>;

  constructor(store: InMemoryDocumentStore<StoredRestaurantSnapshot>) {
    this.#store = store;
  }

  async get(restaurantId: string): Promise<RestaurantView | undefined> {
    const stored = this.#store.get(restaurantId);
    return stored === undefined ? undefined : toView(stored.document);
  }

  async list(criteria: {
    includeClosed: boolean;
    after: string | undefined;
    limit: number;
  }): Promise<readonly RestaurantView[]> {
    return this.#store
      .all()
      .map((stored) => stored.document)
      .filter((restaurant) => criteria.includeClosed || restaurant.isOpen)
      .filter(
        (restaurant) =>
          criteria.after === undefined || restaurant.id > criteria.after,
      )
      .sort((left, right) => (left.id < right.id ? -1 : 1))
      .slice(0, criteria.limit)
      .map(toView);
  }
}

const subjectKey = (subject: Subject): string =>
  `${subject.type}:${subject.id}`;

/**
 * Test authorization graph: explicit registrars and managers; the owner
 * written through {@link OwnerRelationshipWriter} can manage the restaurant;
 * staff written through {@link StaffRelationshipWriter} is recorded.
 */
export class InMemoryRestaurantAuthorization
  implements
    RestaurantAccessControl,
    OwnerRelationshipWriter,
    StaffRelationshipWriter
{
  readonly #registrars = new Set<string>();
  readonly #managers = new Set<string>();
  readonly #owners: Array<{ restaurantId: string; ownerId: string }> = [];
  readonly #staff = new Set<string>();
  /** Every staff write in order, `touch:<restaurantId>:<userId>` or `delete:…`. */
  readonly staffWrites: string[] = [];

  allowRegistration(subject: Subject): this {
    this.#registrars.add(subjectKey(subject));
    return this;
  }

  grantManager(subject: Subject, restaurantId: string): this {
    this.#managers.add(`${subjectKey(subject)}@${restaurantId}`);
    return this;
  }

  get owners(): ReadonlyArray<{ restaurantId: string; ownerId: string }> {
    return this.#owners;
  }

  isStaff(restaurantId: string, userId: string): boolean {
    return this.#staff.has(`${restaurantId}:${userId}`);
  }

  async touchStaff(input: {
    restaurantId: string;
    userId: string;
  }): Promise<void> {
    this.staffWrites.push(`touch:${input.restaurantId}:${input.userId}`);
    this.#staff.add(`${input.restaurantId}:${input.userId}`);
  }

  async deleteStaff(input: {
    restaurantId: string;
    userId: string;
  }): Promise<void> {
    this.staffWrites.push(`delete:${input.restaurantId}:${input.userId}`);
    this.#staff.delete(`${input.restaurantId}:${input.userId}`);
  }

  async canRegisterRestaurant(subject: Subject): Promise<boolean> {
    return this.#registrars.has(subjectKey(subject));
  }

  async canManageRestaurant(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean> {
    return this.#managers.has(`${subjectKey(subject)}@${restaurantId}`);
  }

  async touchOwner(input: {
    restaurantId: string;
    ownerId: string;
  }): Promise<void> {
    const exists = this.#owners.some(
      (owner) =>
        owner.restaurantId === input.restaurantId &&
        owner.ownerId === input.ownerId,
    );
    if (!exists) {
      this.#owners.push({ ...input });
    }
    this.grantManager({ type: "user", id: input.ownerId }, input.restaurantId);
  }
}
