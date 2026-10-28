import type { DeliveryWindow } from "./delivery-windows.ts";
import { UnknownSlotReservationError } from "./errors.ts";

export {
  type DeliveryWindow,
  type DeliveryWindowGrid,
  DeliveryWindows,
} from "./delivery-windows.ts";
export {
  InvalidDeliveryWindowError,
  InvalidDeliveryWindowReason,
  ScheduledOrdersUnavailableError,
  UnknownSlotReservationError,
} from "./errors.ts";

/**
 * Place of one Order in a Schedule Slot (FR-674, FR-675): `held` until the
 * payment is authorized and it is confirmed, with `expiresAt`; `confirmed`
 * never expires.
 */
export type SlotReservation =
  | Readonly<{ orderId: string; status: "held"; expiresAt: Date }>
  | Readonly<{ orderId: string; status: "confirmed" }>;

export type ScheduleSlotSnapshot = Readonly<{
  restaurantId: string;
  window: DeliveryWindow;
  reservations: readonly SlotReservation[];
  /**
   * Orders whose expired hold was purged: a later Confirm re-checks the
   * capacity of this window instead of failing to find it.
   */
  lapsed: readonly string[];
}>;

/** Outcome of a reservation attempt; `full` changes nothing. */
export type ReserveOutcome = "reserved" | "repeated" | "full";

/** Outcome of a confirmation; `full` changes nothing. */
export type ConfirmOutcome = "confirmed" | "repeated" | "full";

/** Stable id of the Schedule Slot of a restaurant window. */
export const scheduleSlotId = (restaurantId: string, start: Date): string =>
  `${restaurantId}|${start.toISOString()}`;

/**
 * Aggregate root of one Delivery Window of a restaurant with its Slot
 * Reservations (FR-674, FR-675, FR-695). A reservation is active when it is
 * `confirmed` or `held` with `expiresAt > now`; the window is full when its
 * active reservations reach the capacity. Expired holds are purged by the
 * next write of the window, no background job runs. The capacity is the
 * current setting of the restaurant, passed to every write: a lowered
 * capacity never drops existing reservations.
 */
export class ScheduleSlot {
  readonly #restaurantId: string;
  readonly #window: DeliveryWindow;
  readonly #version: number;
  #reservations: SlotReservation[];
  #lapsed: string[];

  private constructor(snapshot: ScheduleSlotSnapshot, version: number) {
    this.#restaurantId = snapshot.restaurantId;
    this.#window = snapshot.window;
    this.#reservations = [...snapshot.reservations];
    this.#lapsed = [...snapshot.lapsed];
    this.#version = version;
  }

  /** A window without reservations, never saved. */
  static open(input: {
    restaurantId: string;
    window: DeliveryWindow;
  }): ScheduleSlot {
    return new ScheduleSlot(
      {
        restaurantId: input.restaurantId,
        window: input.window,
        reservations: [],
        lapsed: [],
      },
      0,
    );
  }

  static restore(
    snapshot: ScheduleSlotSnapshot,
    version: number,
  ): ScheduleSlot {
    return new ScheduleSlot(snapshot, version);
  }

  get id(): string {
    return scheduleSlotId(this.#restaurantId, this.#window.start);
  }

  get restaurantId(): string {
    return this.#restaurantId;
  }

  get window(): DeliveryWindow {
    return this.#window;
  }

  /** Version loaded from storage; `0` for a window never saved. */
  get version(): number {
    return this.#version;
  }

  reservationOf(orderId: string): SlotReservation | undefined {
    return this.#reservations.find(
      (reservation) => reservation.orderId === orderId,
    );
  }

  /** Whether the window knows the order: reserved now or with a purged hold. */
  knows(orderId: string): boolean {
    return (
      this.reservationOf(orderId) !== undefined ||
      this.#lapsed.includes(orderId)
    );
  }

  static isActive(reservation: SlotReservation, now: Date): boolean {
    return (
      reservation.status === "confirmed" ||
      reservation.expiresAt.getTime() > now.getTime()
    );
  }

  activeReservations(now: Date): number {
    return this.#reservations.filter((reservation) =>
      ScheduleSlot.isActive(reservation, now),
    ).length;
  }

  /** Room for one more reservation at `now` under `capacity`. */
  isAvailable(now: Date, capacity: number): boolean {
    return this.activeReservations(now) < capacity;
  }

  /**
   * Holds a place for the order until `now + holdTtlMs`. An active
   * reservation of the order is a repeat; a full window changes nothing.
   */
  reserve(input: {
    orderId: string;
    now: Date;
    capacity: number;
    holdTtlMs: number;
  }): ReserveOutcome {
    const existing = this.reservationOf(input.orderId);
    if (existing !== undefined && ScheduleSlot.isActive(existing, input.now)) {
      return "repeated";
    }
    if (!this.isAvailable(input.now, input.capacity)) return "full";
    this.#purge(input.now);
    this.#lapsed = this.#lapsed.filter((orderId) => orderId !== input.orderId);
    this.#reservations.push({
      orderId: input.orderId,
      status: "held",
      expiresAt: new Date(input.now.getTime() + input.holdTtlMs),
    });
    return "reserved";
  }

  /**
   * Confirms the reservation of the order; it never expires afterwards. A
   * confirmed one is a repeat. An expired hold, purged or not, re-checks the
   * capacity: with room it is confirmed, otherwise the window is full and
   * nothing changes.
   */
  confirm(input: {
    orderId: string;
    now: Date;
    capacity: number;
  }): ConfirmOutcome {
    if (!this.knows(input.orderId)) {
      throw new UnknownSlotReservationError(input.orderId);
    }
    const existing = this.reservationOf(input.orderId);
    if (existing?.status === "confirmed") return "repeated";
    if (
      (existing === undefined || !ScheduleSlot.isActive(existing, input.now)) &&
      !this.isAvailable(input.now, input.capacity)
    ) {
      return "full";
    }
    this.#purge(input.now);
    this.#lapsed = this.#lapsed.filter((orderId) => orderId !== input.orderId);
    this.#reservations = [
      ...this.#reservations.filter(
        (reservation) => reservation.orderId !== input.orderId,
      ),
      { orderId: input.orderId, status: "confirmed" },
    ];
    return "confirmed";
  }

  /** Removes the order from the window; `false` when it was not there. */
  release(input: { orderId: string; now: Date }): boolean {
    if (!this.knows(input.orderId)) return false;
    this.#purge(input.now);
    this.#reservations = this.#reservations.filter(
      (reservation) => reservation.orderId !== input.orderId,
    );
    this.#lapsed = this.#lapsed.filter((orderId) => orderId !== input.orderId);
    return true;
  }

  /** Moves expired holds out of the reservations. */
  #purge(now: Date): void {
    for (const reservation of this.#reservations) {
      if (!ScheduleSlot.isActive(reservation, now)) {
        this.#lapsed.push(reservation.orderId);
      }
    }
    this.#reservations = this.#reservations.filter((reservation) =>
      ScheduleSlot.isActive(reservation, now),
    );
  }

  toSnapshot(): ScheduleSlotSnapshot {
    return {
      restaurantId: this.#restaurantId,
      window: { ...this.#window },
      reservations: [...this.#reservations],
      lapsed: [...this.#lapsed],
    };
  }
}

/**
 * Repository contract of {@link ScheduleSlot}. `save` writes with optimistic
 * concurrency on the loaded version (a new window included) and returns the
 * committed version; an order is held by at most one window.
 */
export interface ScheduleSlotRepository {
  get(restaurantId: string, start: Date): Promise<ScheduleSlot | undefined>;
  /** The window that knows the order: reserved or with a purged hold. */
  findByOrderId(orderId: string): Promise<ScheduleSlot | undefined>;
  /** Saved windows of the restaurant starting in `[from, to]`. */
  listBetween(
    restaurantId: string,
    from: Date,
    to: Date,
  ): Promise<readonly ScheduleSlot[]>;
  save(slot: ScheduleSlot): Promise<number>;
}
