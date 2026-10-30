import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { UnitOfWork } from "../../../../pkg/unit-of-work/unit-of-work.ts";
import type { Clock } from "../../../../pkg/usecase/clock.ts";
import {
  ConcurrencyConflictError,
  NotFoundError,
} from "../../../../pkg/usecase/errors.ts";
import type { RestaurantRepository } from "../../../restaurant/domains/restaurant/restaurant.ts";
import {
  type DeliveryWindow,
  type ScheduleSlotRepository,
  UnknownSlotReservationError,
} from "../../domains/scheduleslot/scheduleslot.ts";
import type { SlotReservationAccess } from "../access-control.ts";
import { RESERVE_ATTEMPTS } from "../reserve-scheduled-slot/reserve-scheduled-slot.ts";

export type ConfirmScheduledSlot = Readonly<{
  /** `service` identity of the `orders` fulfillment process. */
  subject: Subject;
  orderId: string;
}>;

/**
 * The hold expired and the window filled up meanwhile: a business answer
 * without alternatives, the order already has its window.
 */
export type ConfirmScheduledSlotResult =
  | Readonly<{ outcome: "confirmed"; window: DeliveryWindow }>
  | Readonly<{ outcome: "slot_unavailable" }>;

/**
 * Confirms the Slot Reservation of the order after its payment is
 * authorized (FR-675): `held` becomes `confirmed` and never expires; a repeat
 * is a no-op. An expired hold re-checks the current capacity of the window.
 * An order no window knows is not found.
 */
export class ConfirmScheduledSlotHandler {
  readonly #accessControl: SlotReservationAccess;
  readonly #unitOfWork: UnitOfWork;
  readonly #restaurants: Pick<RestaurantRepository, "get">;
  readonly #slots: ScheduleSlotRepository;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: SlotReservationAccess;
    unitOfWork: UnitOfWork;
    restaurants: Pick<RestaurantRepository, "get">;
    slots: ScheduleSlotRepository;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#unitOfWork = dependencies.unitOfWork;
    this.#restaurants = dependencies.restaurants;
    this.#slots = dependencies.slots;
    this.#clock = dependencies.clock;
  }

  async execute(
    command: ConfirmScheduledSlot,
  ): Promise<ConfirmScheduledSlotResult> {
    await requirePermission(
      this.#accessControl.canCoordinateFulfillment(command.subject),
      "confirm scheduled slot",
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
    command: ConfirmScheduledSlot,
  ): Promise<ConfirmScheduledSlotResult> {
    const now = this.#clock.now();
    const slot = await this.#slots.findByOrderId(command.orderId);
    if (slot === undefined) {
      throw new UnknownSlotReservationError(command.orderId);
    }
    const restaurant = await this.#restaurants.get(slot.restaurantId);
    if (restaurant === undefined) {
      throw new NotFoundError("Restaurant", slot.restaurantId);
    }
    // A restaurant that stopped taking scheduled orders has no room for an
    // expired hold; an active hold is confirmed regardless.
    const settings = restaurant.scheduledOrders;
    const outcome = slot.confirm({
      orderId: command.orderId,
      now,
      capacity: settings === undefined ? 0 : settings.slotCapacity,
    });
    if (outcome === "full") return { outcome: "slot_unavailable" };
    if (outcome === "confirmed") await this.#slots.save(slot);
    return { outcome: "confirmed", window: slot.window };
  }
}
