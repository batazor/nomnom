import type { Clock } from "../../../../pkg/application/clock.ts";
import {
  ConcurrencyConflictError,
  NotFoundError,
} from "../../../../pkg/application/errors.ts";
import { requirePermission } from "../../../../pkg/application/require-permission.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import type { UnitOfWork } from "../../../../pkg/application/unit-of-work.ts";
import type {
  Restaurant,
  RestaurantRepository,
} from "../../../restaurant/domains/restaurant/restaurant.ts";
import {
  type DeliveryWindow,
  type DeliveryWindows,
  ScheduledOrdersUnavailableError,
  ScheduleSlot,
  type ScheduleSlotRepository,
} from "../../domains/scheduleslot/scheduleslot.ts";
import type { SlotReservationAccess } from "../access-control.ts";
import { windowAvailability } from "../window-availability.ts";

export type ReserveScheduledSlot = Readonly<{
  /** `service` identity of the `orders` fulfillment process. */
  subject: Subject;
  restaurantId: string;
  windowStart: Date;
  /** Pre-generated id of the Order: the idempotency key of the reservation. */
  orderId: string;
}>;

/** A full window is a business answer with the nearest available windows. */
export type ReserveScheduledSlotResult =
  | Readonly<{ outcome: "reserved"; window: DeliveryWindow }>
  | Readonly<{
      outcome: "slot_unavailable";
      alternatives: readonly DeliveryWindow[];
    }>;

/** Attempts of one reservation against concurrent writes of the window. */
export const RESERVE_ATTEMPTS = 5;

/**
 * Holds a place of the order in a Delivery Window of the restaurant for
 * `holdTtlMs` (FR-674, FR-695). A window off the grid, before now + MIN_LEAD,
 * beyond the horizon or outside the schedule is invalid; a restaurant without
 * settings or not accepting orders now takes no scheduled orders. A full
 * window answers with at most `alternatives` nearest available windows. A
 * repeat with the same order returns its active reservation. Concurrent
 * reservations of one window conflict on its version and are retried, so the
 * capacity is never exceeded.
 */
export class ReserveScheduledSlotHandler {
  readonly #accessControl: SlotReservationAccess;
  readonly #unitOfWork: UnitOfWork;
  readonly #restaurants: Pick<RestaurantRepository, "get">;
  readonly #slots: ScheduleSlotRepository;
  readonly #windows: DeliveryWindows;
  readonly #holdTtlMs: number;
  readonly #alternatives: number;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: SlotReservationAccess;
    unitOfWork: UnitOfWork;
    restaurants: Pick<RestaurantRepository, "get">;
    slots: ScheduleSlotRepository;
    windows: DeliveryWindows;
    holdTtlMs: number;
    alternatives: number;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#unitOfWork = dependencies.unitOfWork;
    this.#restaurants = dependencies.restaurants;
    this.#slots = dependencies.slots;
    this.#windows = dependencies.windows;
    this.#holdTtlMs = dependencies.holdTtlMs;
    this.#alternatives = dependencies.alternatives;
    this.#clock = dependencies.clock;
  }

  async execute(
    command: ReserveScheduledSlot,
  ): Promise<ReserveScheduledSlotResult> {
    await requirePermission(
      this.#accessControl.canCoordinateFulfillment(command.subject),
      "reserve scheduled slot",
    );
    for (let attempt = 1; ; attempt += 1) {
      try {
        return await this.#unitOfWork.run(() => this.#attempt(command));
      } catch (error) {
        if (
          !(error instanceof ConcurrencyConflictError) ||
          attempt >= RESERVE_ATTEMPTS
        ) {
          throw error;
        }
      }
    }
  }

  async #attempt(
    command: ReserveScheduledSlot,
  ): Promise<ReserveScheduledSlotResult> {
    const now = this.#clock.now();
    const known = await this.#slots.findByOrderId(command.orderId);
    const reserved = known?.reservationOf(command.orderId);
    if (
      known !== undefined &&
      reserved !== undefined &&
      ScheduleSlot.isActive(reserved, now)
    ) {
      return { outcome: "reserved", window: known.window };
    }
    const restaurant = await this.#restaurants.get(command.restaurantId);
    if (restaurant === undefined) {
      throw new NotFoundError("Restaurant", command.restaurantId);
    }
    const settings = restaurant.scheduledOrders;
    if (settings === undefined || !restaurant.acceptsOrdersAt(now)) {
      throw new ScheduledOrdersUnavailableError(command.restaurantId);
    }
    const window = this.#windows.require(
      this.#windows.offered(restaurant, now),
      command.windowStart,
      now,
    );
    // A lapsed hold of the order in another window is dropped there.
    if (
      known !== undefined &&
      known.window.start.getTime() !== window.start.getTime()
    ) {
      known.release({ orderId: command.orderId, now });
      await this.#slots.save(known);
    }
    const slot =
      known?.window.start.getTime() === window.start.getTime()
        ? known
        : await this.#slot(command.restaurantId, window);
    const outcome = slot.reserve({
      orderId: command.orderId,
      now,
      capacity: settings.slotCapacity,
      holdTtlMs: this.#holdTtlMs,
    });
    if (outcome === "full") {
      return {
        outcome: "slot_unavailable",
        alternatives: await this.#nearestAvailable(restaurant, window, now),
      };
    }
    if (outcome === "reserved") await this.#slots.save(slot);
    return { outcome: "reserved", window: slot.window };
  }

  async #slot(
    restaurantId: string,
    window: DeliveryWindow,
  ): Promise<ScheduleSlot> {
    const saved = await this.#slots.get(restaurantId, window.start);
    return saved === undefined
      ? ScheduleSlot.open({ restaurantId, window })
      : saved;
  }

  /** Available windows closest to `window`, earlier first on a tie, by start. */
  async #nearestAvailable(
    restaurant: Restaurant,
    window: DeliveryWindow,
    now: Date,
  ): Promise<readonly DeliveryWindow[]> {
    const target = window.start.getTime();
    const distance = (candidate: DeliveryWindow): number =>
      Math.abs(candidate.start.getTime() - target);
    return (
      await windowAvailability(
        { windows: this.#windows, slots: this.#slots },
        restaurant,
        now,
      )
    )
      .filter(
        (candidate) =>
          candidate.available && candidate.window.start.getTime() !== target,
      )
      .map((candidate) => candidate.window)
      .sort(
        (a, b) =>
          distance(a) - distance(b) || a.start.getTime() - b.start.getTime(),
      )
      .slice(0, this.#alternatives)
      .sort((a, b) => a.start.getTime() - b.start.getTime());
  }
}
