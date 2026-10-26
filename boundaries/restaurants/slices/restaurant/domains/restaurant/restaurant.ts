import { InvalidRestaurantError, InvalidRestaurantReason } from "./errors.ts";
import type { RestaurantEvent } from "./events.ts";

export * from "./acceptance.ts";
export * from "./errors.ts";
export type {
  RestaurantClosed,
  RestaurantEvent,
  RestaurantOpened,
  RestaurantRegistered,
  RestaurantRenamed,
} from "./events.ts";

export type RestaurantSnapshot = Readonly<{
  id: string;
  name: string;
  ownerId: string;
  isOpen: boolean;
  /** User ids of the restaurant staff, sorted; source of `restaurant#staff`. */
  staff: readonly string[];
  /**
   * Technical: ZedToken of the last relationship write of this restaurant,
   * opaque to the domain. Absent until the first write is recorded.
   */
  authorizationRevision?: string;
}>;

/**
 * Persisted form of {@link RestaurantSnapshot}. Restaurants stored before
 * staff existed are restored without staff; snapshots without an
 * authorization revision are restored without one.
 */
export type StoredRestaurantSnapshot = Omit<RestaurantSnapshot, "staff"> &
  Readonly<{ staff?: readonly string[] }>;

const restaurantName = (name: string): string => {
  const normalized = name.trim();
  if (normalized.length === 0) {
    throw new InvalidRestaurantError(InvalidRestaurantReason.EmptyName);
  }
  return normalized;
};

/** Staff member: a non-empty trimmed user id. */
const staffMember = (userId: string): string => {
  const normalized = userId.trim();
  if (normalized.length === 0) {
    throw new InvalidRestaurantError(InvalidRestaurantReason.EmptyStaff);
  }
  return normalized;
};

/**
 * Aggregate root of a restaurant as a managed resource. A newly registered
 * restaurant is closed for new Restaurant Orders until it is opened.
 */
export class Restaurant {
  readonly #id: string;
  readonly #ownerId: string;
  readonly #version: number;
  #name: string;
  #isOpen: boolean;
  readonly #staff: Set<string>;
  #authorizationRevision: string | undefined;
  #events: RestaurantEvent[] = [];

  private constructor(snapshot: StoredRestaurantSnapshot, version: number) {
    this.#id = snapshot.id;
    this.#name = snapshot.name;
    this.#ownerId = snapshot.ownerId;
    this.#isOpen = snapshot.isOpen;
    this.#staff = new Set(snapshot.staff ?? []);
    this.#authorizationRevision = snapshot.authorizationRevision;
    this.#version = version;
  }

  static register(input: {
    id: string;
    name: string;
    ownerId: string;
    registeredAt: Date;
  }): Restaurant {
    const ownerId = input.ownerId.trim();
    if (ownerId.length === 0) {
      throw new InvalidRestaurantError(InvalidRestaurantReason.EmptyOwner);
    }
    const restaurant = new Restaurant(
      {
        id: input.id,
        name: restaurantName(input.name),
        ownerId,
        isOpen: false,
        staff: [],
      },
      0,
    );
    restaurant.#events.push({
      type: "RestaurantRegistered",
      restaurantId: restaurant.#id,
      name: restaurant.#name,
      ownerId,
      occurredAt: input.registeredAt,
    });
    return restaurant;
  }

  static restore(
    snapshot: StoredRestaurantSnapshot,
    version: number,
  ): Restaurant {
    return new Restaurant(snapshot, version);
  }

  get id(): string {
    return this.#id;
  }

  get name(): string {
    return this.#name;
  }

  get ownerId(): string {
    return this.#ownerId;
  }

  get isOpen(): boolean {
    return this.#isOpen;
  }

  /** User ids of the staff, sorted. */
  get staff(): readonly string[] {
    return [...this.#staff].sort();
  }

  /** ZedToken of the last recorded relationship write, if any. */
  get authorizationRevision(): string | undefined {
    return this.#authorizationRevision;
  }

  /**
   * Records the revision (ZedToken) of a relationship write of this
   * restaurant. Technical state: no domain event, opaque to the aggregate.
   */
  recordAuthorizationRevision(revision: string): void {
    if (revision.length === 0) {
      throw new Error("authorization revision must not be empty");
    }
    this.#authorizationRevision = revision;
  }

  /** Version loaded from storage; `0` for a restaurant that was never saved. */
  get version(): number {
    return this.#version;
  }

  /** Returns `false` when the name is already current. */
  rename(name: string, renamedAt: Date): boolean {
    const normalized = restaurantName(name);
    if (normalized === this.#name) {
      return false;
    }
    this.#name = normalized;
    this.#events.push({
      type: "RestaurantRenamed",
      restaurantId: this.#id,
      name: normalized,
      occurredAt: renamedAt,
    });
    return true;
  }

  /** Allows new Restaurant Orders; returns `false` when already open. */
  open(openedAt: Date): boolean {
    if (this.#isOpen) {
      return false;
    }
    this.#isOpen = true;
    this.#events.push({
      type: "RestaurantOpened",
      restaurantId: this.#id,
      occurredAt: openedAt,
    });
    return true;
  }

  /**
   * Stops new Restaurant Orders; existing orders are not affected. Returns
   * `false` when already closed.
   */
  close(closedAt: Date): boolean {
    if (!this.#isOpen) {
      return false;
    }
    this.#isOpen = false;
    this.#events.push({
      type: "RestaurantClosed",
      restaurantId: this.#id,
      occurredAt: closedAt,
    });
    return true;
  }

  /**
   * Adds the user to the staff. Staff is an idempotent set: returns `false`
   * when the user is already staff.
   */
  assignStaff(userId: string): boolean {
    const member = staffMember(userId);
    if (this.#staff.has(member)) {
      return false;
    }
    this.#staff.add(member);
    return true;
  }

  /** Removes the user from the staff; returns `false` when the user is not staff. */
  revokeStaff(userId: string): boolean {
    return this.#staff.delete(staffMember(userId));
  }

  toSnapshot(): RestaurantSnapshot {
    return {
      id: this.#id,
      name: this.#name,
      ownerId: this.#ownerId,
      isOpen: this.#isOpen,
      staff: this.staff,
      ...(this.#authorizationRevision === undefined
        ? {}
        : { authorizationRevision: this.#authorizationRevision }),
    };
  }

  pullEvents(): RestaurantEvent[] {
    const events = this.#events;
    this.#events = [];
    return events;
  }
}

/**
 * Repository contract of {@link Restaurant}. `save` writes with optimistic
 * concurrency on the loaded version and returns the committed version.
 */
export interface RestaurantRepository {
  get(id: string): Promise<Restaurant | undefined>;
  save(restaurant: Restaurant): Promise<number>;
}
