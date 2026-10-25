export const RestaurantAcceptanceReason = {
  OutsideOpeningHours: "outside_opening_hours",
  ScheduleException: "schedule_exception",
  CapacityExhausted: "capacity_exhausted",
} as const;

export type RestaurantAcceptanceReason =
  (typeof RestaurantAcceptanceReason)[keyof typeof RestaurantAcceptanceReason];

export type RestaurantAcceptanceContext = Readonly<{
  now: Date;
  opensAt: Date;
  closesAt: Date;
  currentOrders: number;
  maximumOrders: number;
  scheduleException: boolean;
}>;

export type RestaurantAcceptanceResult = Readonly<{
  satisfied: boolean;
  reasons: readonly RestaurantAcceptanceReason[];
}>;

export const RestaurantAcceptingOrders = (
  context: RestaurantAcceptanceContext,
): RestaurantAcceptanceResult => {
  const reasons: RestaurantAcceptanceReason[] = [];
  const now = context.now.getTime();
  if (now < context.opensAt.getTime() || now >= context.closesAt.getTime()) {
    reasons.push(RestaurantAcceptanceReason.OutsideOpeningHours);
  }
  if (context.scheduleException) {
    reasons.push(RestaurantAcceptanceReason.ScheduleException);
  }
  if (context.currentOrders >= context.maximumOrders) {
    reasons.push(RestaurantAcceptanceReason.CapacityExhausted);
  }
  return { satisfied: reasons.length === 0, reasons };
};

export type RestaurantSnapshot = Readonly<{
  id: string;
  name: string;
  ownerId: string;
  isOpen: boolean;
  /** Pickup address of Restaurant Orders; empty only for legacy restaurants. */
  address: string;
}>;

/**
 * Persisted form of {@link RestaurantSnapshot}. Restaurants stored before the
 * address became required have none and are restored with an empty address.
 */
export type StoredRestaurantSnapshot = Omit<RestaurantSnapshot, "address"> &
  Readonly<{ address?: string }>;

type RestaurantEventBase = Readonly<{
  restaurantId: string;
  occurredAt: Date;
}>;

export type RestaurantRegistered = RestaurantEventBase &
  Readonly<{
    type: "RestaurantRegistered";
    name: string;
    ownerId: string;
    address: string;
  }>;
export type RestaurantRenamed = RestaurantEventBase &
  Readonly<{ type: "RestaurantRenamed"; name: string }>;
export type RestaurantAddressChanged = RestaurantEventBase &
  Readonly<{ type: "RestaurantAddressChanged"; address: string }>;
export type RestaurantOpened = RestaurantEventBase &
  Readonly<{ type: "RestaurantOpened" }>;
export type RestaurantClosed = RestaurantEventBase &
  Readonly<{ type: "RestaurantClosed" }>;

export type RestaurantEvent =
  | RestaurantRegistered
  | RestaurantRenamed
  | RestaurantAddressChanged
  | RestaurantOpened
  | RestaurantClosed;

export const InvalidRestaurantReason = {
  EmptyName: "empty_name",
  EmptyOwner: "empty_owner",
  EmptyAddress: "empty_address",
} as const;

export type InvalidRestaurantReason =
  (typeof InvalidRestaurantReason)[keyof typeof InvalidRestaurantReason];

export class InvalidRestaurantError extends Error {
  readonly code = "invalid_restaurant";
  readonly reason: InvalidRestaurantReason;

  constructor(reason: InvalidRestaurantReason) {
    super(`invalid restaurant: ${reason}`);
    this.name = "InvalidRestaurantError";
    this.reason = reason;
  }
}

const restaurantName = (name: string): string => {
  const normalized = name.trim();
  if (normalized.length === 0) {
    throw new InvalidRestaurantError(InvalidRestaurantReason.EmptyName);
  }
  return normalized;
};

/** Pickup address: a non-empty trimmed string. */
const restaurantAddress = (address: string): string => {
  const normalized = address.trim();
  if (normalized.length === 0) {
    throw new InvalidRestaurantError(InvalidRestaurantReason.EmptyAddress);
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
  #address: string;
  #events: RestaurantEvent[] = [];

  private constructor(snapshot: StoredRestaurantSnapshot, version: number) {
    this.#id = snapshot.id;
    this.#name = snapshot.name;
    this.#ownerId = snapshot.ownerId;
    this.#isOpen = snapshot.isOpen;
    this.#address = snapshot.address ?? "";
    this.#version = version;
  }

  static register(input: {
    id: string;
    name: string;
    ownerId: string;
    address: string;
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
        address: restaurantAddress(input.address),
      },
      0,
    );
    restaurant.#events.push({
      type: "RestaurantRegistered",
      restaurantId: restaurant.#id,
      name: restaurant.#name,
      ownerId,
      address: restaurant.#address,
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

  /** Pickup address; empty for a restaurant registered before it was required. */
  get address(): string {
    return this.#address;
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

  /** Returns `false` when the address is already current. */
  changeAddress(address: string, changedAt: Date): boolean {
    const normalized = restaurantAddress(address);
    if (normalized === this.#address) {
      return false;
    }
    this.#address = normalized;
    this.#events.push({
      type: "RestaurantAddressChanged",
      restaurantId: this.#id,
      address: normalized,
      occurredAt: changedAt,
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

  toSnapshot(): RestaurantSnapshot {
    return {
      id: this.#id,
      name: this.#name,
      ownerId: this.#ownerId,
      isOpen: this.#isOpen,
      address: this.#address,
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
