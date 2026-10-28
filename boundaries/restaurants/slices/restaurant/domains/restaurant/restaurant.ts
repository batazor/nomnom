import { Money, type MoneyValue } from "../../../menu/domains/menu/vo/money.ts";
import { InvalidRestaurantError, InvalidRestaurantReason } from "./errors.ts";
import type { RestaurantEvent } from "./events.ts";
import { MarketId } from "./vo/market-id.ts";
import {
  OpeningHours,
  type OpeningInterval,
  type OpeningPeriod,
} from "./vo/opening-hours.ts";
import {
  ScheduledOrderSettings,
  type ScheduledOrderSettingsValue,
} from "./vo/scheduled-order-settings.ts";
import { TimeZone } from "./vo/time-zone.ts";

export * from "./acceptance.ts";
export * from "./errors.ts";
export type {
  RestaurantClosed,
  RestaurantEvent,
  RestaurantOpened,
  RestaurantRegistered,
  RestaurantRenamed,
} from "./events.ts";
export { InvalidMarketIdError, MarketId } from "./vo/market-id.ts";
export {
  OpeningHours,
  type OpeningInterval,
  type OpeningPeriod,
} from "./vo/opening-hours.ts";
export {
  ScheduledOrderSettings,
  type ScheduledOrderSettingsValue,
} from "./vo/scheduled-order-settings.ts";
export { type LocalDate, TimeZone } from "./vo/time-zone.ts";

export type RestaurantSnapshot = Readonly<{
  id: string;
  name: string;
  ownerId: string;
  /** Market of the restaurant (ADR 41), immutable. */
  marketId: string;
  /** IANA time zone of the restaurant (FR-201), immutable. */
  timeZone: string;
  /** Weekly opening hours on the wall clock (FR-696); empty — never by schedule. */
  openingHours: readonly OpeningInterval[];
  isOpen: boolean;
  /** User ids of the restaurant staff, sorted; source of `restaurant#staff`. */
  staff: readonly string[];
  /**
   * Technical: ZedToken of the last relationship write of this restaurant,
   * opaque to the domain. Absent until the first write is recorded.
   */
  authorizationRevision?: string;
  /**
   * Fee of Premium Priority in the currency of the restaurant (FR-202);
   * absent when the restaurant offers no premium.
   */
  premiumFee?: MoneyValue;
  /**
   * Settings of scheduled orders (FR-672); absent when the restaurant takes
   * no scheduled orders.
   */
  scheduledOrders?: ScheduledOrderSettingsValue;
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

const ZERO_AMOUNT = /^0+(\.0+)?$/;

/** Premium fee: exact money greater than zero. */
const premiumFee = (value: MoneyValue): Money => {
  const fee = Money.from(value);
  if (ZERO_AMOUNT.test(fee.amount)) {
    throw new InvalidRestaurantError(
      InvalidRestaurantReason.NonPositivePremiumFee,
    );
  }
  return fee;
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
  readonly #marketId: MarketId;
  readonly #timeZone: TimeZone;
  #openingHours: OpeningHours;
  readonly #version: number;
  #name: string;
  #isOpen: boolean;
  readonly #staff: Set<string>;
  #authorizationRevision: string | undefined;
  #premiumFee: Money | undefined;
  #scheduledOrders: ScheduledOrderSettings | undefined;
  #events: RestaurantEvent[] = [];

  private constructor(snapshot: StoredRestaurantSnapshot, version: number) {
    this.#id = snapshot.id;
    this.#name = snapshot.name;
    this.#ownerId = snapshot.ownerId;
    this.#marketId = MarketId.of(snapshot.marketId);
    this.#timeZone = TimeZone.of(snapshot.timeZone);
    this.#openingHours = OpeningHours.of(snapshot.openingHours);
    this.#isOpen = snapshot.isOpen;
    this.#staff = new Set(snapshot.staff ?? []);
    this.#authorizationRevision = snapshot.authorizationRevision;
    this.#premiumFee =
      snapshot.premiumFee === undefined
        ? undefined
        : Money.from(snapshot.premiumFee);
    this.#scheduledOrders =
      snapshot.scheduledOrders === undefined
        ? undefined
        : ScheduledOrderSettings.of(snapshot.scheduledOrders);
    this.#version = version;
  }

  static register(input: {
    id: string;
    name: string;
    ownerId: string;
    /** Market the restaurant serves; set once at registration (ADR 41). */
    marketId: string;
    /** IANA time zone of the restaurant; set once at registration (FR-201). */
    timeZone: string;
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
        marketId: MarketId.of(input.marketId).value,
        timeZone: TimeZone.of(input.timeZone).value,
        openingHours: [],
        isOpen: false,
        staff: [],
      },
      0,
    );
    restaurant.#events.push({
      type: "RestaurantRegistered",
      restaurantId: restaurant.#id,
      marketId: restaurant.#marketId.value,
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

  get marketId(): MarketId {
    return this.#marketId;
  }

  get isOpen(): boolean {
    return this.#isOpen;
  }

  get timeZone(): TimeZone {
    return this.#timeZone;
  }

  get openingHours(): OpeningHours {
    return this.#openingHours;
  }

  /**
   * Replaces the weekly schedule (FR-696). Returns `false` when it is already
   * current.
   */
  setOpeningHours(intervals: readonly OpeningInterval[]): boolean {
    const next = OpeningHours.of(intervals);
    if (next.equals(this.#openingHours)) {
      return false;
    }
    this.#openingHours = next;
    return true;
  }

  /** Schedule period running at `at` (FR-699). */
  openingPeriodAt(at: Date): OpeningPeriod | undefined {
    return this.#openingHours.periodAt(this.#timeZone, at);
  }

  /** Schedule periods overlapping `[from, to)`, by opening (FR-698). */
  openingPeriodsBetween(from: Date, to: Date): OpeningPeriod[] {
    return this.#openingHours.periodsBetween(this.#timeZone, from, to);
  }

  /** Opened by the flag and inside the schedule at `at` (FR-699). */
  acceptsOrdersAt(at: Date): boolean {
    return this.#isOpen && this.openingPeriodAt(at) !== undefined;
  }

  /** Settings of scheduled orders; `undefined` when none are taken. */
  get scheduledOrders(): ScheduledOrderSettings | undefined {
    return this.#scheduledOrders;
  }

  /**
   * Sets the settings of scheduled orders together, or clears them with
   * `undefined` (FR-672). Existing reservations are not affected. Returns
   * `false` when they are already current.
   */
  setScheduledOrderSettings(
    settings: ScheduledOrderSettingsValue | undefined,
  ): boolean {
    const next =
      settings === undefined ? undefined : ScheduledOrderSettings.of(settings);
    const current = this.#scheduledOrders;
    if (
      next === undefined
        ? current === undefined
        : current !== undefined && next.equals(current)
    ) {
      return false;
    }
    this.#scheduledOrders = next;
    return true;
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
      marketId: this.#marketId.value,
      name: normalized,
      occurredAt: renamedAt,
    });
    return true;
  }

  /** Fee of Premium Priority; `undefined` when no premium is offered. */
  get premiumFee(): Money | undefined {
    return this.#premiumFee;
  }

  /**
   * Sets the premium fee, or removes the premium option with `undefined`.
   * Returns `false` when the fee is already current.
   */
  changePremiumFee(fee: MoneyValue | undefined): boolean {
    const next = fee === undefined ? undefined : premiumFee(fee);
    const current = this.#premiumFee;
    if (
      next === undefined
        ? current === undefined
        : current !== undefined &&
          next.equals(current) &&
          next.amount === current.amount
    ) {
      return false;
    }
    this.#premiumFee = next;
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
      marketId: this.#marketId.value,
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
      marketId: this.#marketId.value,
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
      marketId: this.#marketId.value,
      timeZone: this.#timeZone.value,
      openingHours: this.#openingHours.intervals,
      isOpen: this.#isOpen,
      staff: this.staff,
      ...(this.#authorizationRevision === undefined
        ? {}
        : { authorizationRevision: this.#authorizationRevision }),
      ...(this.#premiumFee === undefined
        ? {}
        : { premiumFee: this.#premiumFee.toValue() }),
      ...(this.#scheduledOrders === undefined
        ? {}
        : { scheduledOrders: this.#scheduledOrders.toValue() }),
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
