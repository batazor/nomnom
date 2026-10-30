import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { UnitOfWork } from "../../../../pkg/unit-of-work/unit-of-work.ts";
import type { Clock } from "../../../../pkg/usecase/clock.ts";
import { ConcurrencyConflictError } from "../../../../pkg/usecase/errors.ts";
import type { ScheduleSlotRepository } from "../../domains/scheduleslot/scheduleslot.ts";
import type { SlotReservationAccess } from "../access-control/access-control.ts";
import { RESERVE_ATTEMPTS } from "../reserve-scheduled-slot/reserve-scheduled-slot.ts";

export type ReleaseScheduledSlot = Readonly<{
  /** `service` identity of the `orders` fulfillment process. */
  subject: Subject;
  orderId: string;
}>;

/**
 * Compensation of `orders` (FR-675): removes the Slot Reservation of the
 * order. An order no window knows is a successful no-op, so a repeat leaves
 * the window unchanged. Returns whether a reservation was removed.
 */
export class ReleaseScheduledSlotHandler {
  readonly #accessControl: SlotReservationAccess;
  readonly #unitOfWork: UnitOfWork;
  readonly #slots: ScheduleSlotRepository;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: SlotReservationAccess;
    unitOfWork: UnitOfWork;
    slots: ScheduleSlotRepository;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#unitOfWork = dependencies.unitOfWork;
    this.#slots = dependencies.slots;
    this.#clock = dependencies.clock;
  }

  async execute(command: ReleaseScheduledSlot): Promise<boolean> {
    await requirePermission(
      this.#accessControl.canCoordinateFulfillment(command.subject),
      "release scheduled slot",
    );
    for (let attempt = 1; ; attempt += 1) {
      try {
        return await this.#unitOfWork.run(async () => {
          const slot = await this.#slots.findByOrderId(command.orderId);
          if (
            slot === undefined ||
            !slot.release({ orderId: command.orderId, now: this.#clock.now() })
          ) {
            return false;
          }
          await this.#slots.save(slot);
          return true;
        });
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
}
