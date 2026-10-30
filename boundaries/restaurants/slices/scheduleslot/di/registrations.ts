// Awilix registrations of the `scheduleslot` slice (ADR 5): the MongoDB
// repository of Schedule Slots, the Delivery Window grid from
// SCHEDULED_SLOT__* and the handlers exposed through transport.
import { type AwilixContainer, asFunction } from "awilix";
import type { Db } from "mongodb";
import type { Registrations } from "../../../di/container.ts";
import type { DatabaseUnitOfWork } from "../../../infrastructure/repository/unit-of-work.ts";
import type { ScheduledSlotConfig } from "../../../pkg/config/config.ts";
import {
  command,
  query,
  type UseCase,
} from "../../../pkg/telemetry/usecase.ts";
import type { Clock } from "../../../pkg/usecase/clock.ts";
import type { RestaurantRepository } from "../../restaurant/domains/restaurant/restaurant.ts";
import type { SlotReservationAccess } from "../applications/access-control/access-control.ts";
import { ConfirmScheduledSlotHandler } from "../applications/confirm-scheduled-slot/confirm-scheduled-slot.ts";
import { ListScheduledSlotsHandler } from "../applications/list-scheduled-slots/list-scheduled-slots.ts";
import { ReleaseScheduledSlotHandler } from "../applications/release-scheduled-slot/release-scheduled-slot.ts";
import { ReserveScheduledSlotHandler } from "../applications/reserve-scheduled-slot/reserve-scheduled-slot.ts";
import { DeliveryWindows } from "../domains/scheduleslot/scheduleslot.ts";
import {
  createScheduleSlotCollection,
  DatabaseScheduleSlotRepository,
} from "../infrastructure/repository/schedule-slots.ts";

/** Application-scope dependencies the slice takes from the process. */
export interface ScheduleSlotDependencies {
  readonly db: Db;
  readonly unitOfWork: DatabaseUnitOfWork;
  readonly clock: Clock;
  readonly scheduledSlotConfig: ScheduledSlotConfig;
  /** Schedule and settings of scheduled orders of the `restaurant` slice. */
  readonly restaurants: RestaurantRepository;
  readonly slotReservationAccessControl: SlotReservationAccess;
}

/** What the slice provides to the process. */
export interface ScheduleSlotCradle {
  readonly scheduleSlots: DatabaseScheduleSlotRepository;
  readonly deliveryWindows: DeliveryWindows;
  readonly listScheduledSlots: ListScheduledSlotsHandler;
  readonly reserveScheduledSlot: ReserveScheduledSlotHandler;
  readonly confirmScheduledSlot: ConfirmScheduledSlotHandler;
  readonly releaseScheduledSlot: ReleaseScheduledSlotHandler;
}

/** Use cases of the slice with their telemetry names, by cradle key. */
export const SCHEDULE_SLOT_USE_CASES = {
  listScheduledSlots: query("list_scheduled_slots"),
  reserveScheduledSlot: command("reserve_scheduled_slot"),
  confirmScheduledSlot: command("confirm_scheduled_slot"),
  releaseScheduledSlot: command("release_scheduled_slot"),
} as const satisfies Partial<Record<keyof ScheduleSlotCradle, UseCase>>;

type Cradle = ScheduleSlotDependencies & ScheduleSlotCradle;

export const registerScheduleSlot = (
  container: AwilixContainer<Cradle>,
): void => {
  const registrations: Registrations<ScheduleSlotCradle> = {
    scheduleSlots: asFunction(
      ({ db, unitOfWork }: Cradle) =>
        new DatabaseScheduleSlotRepository(
          createScheduleSlotCollection(db, unitOfWork),
        ),
    ).singleton(),
    deliveryWindows: asFunction(
      ({ scheduledSlotConfig }: Cradle) =>
        new DeliveryWindows({
          widthMs: scheduledSlotConfig.widthMs,
          minLeadMs: scheduledSlotConfig.minLeadMs,
          maxHorizonMs: scheduledSlotConfig.maxHorizonMs,
        }),
    ).singleton(),
    listScheduledSlots: asFunction(
      ({ restaurants, scheduleSlots, deliveryWindows, clock }: Cradle) =>
        new ListScheduledSlotsHandler({
          restaurants,
          slots: scheduleSlots,
          windows: deliveryWindows,
          clock,
        }),
    ).singleton(),
    reserveScheduledSlot: asFunction(
      ({
        slotReservationAccessControl,
        unitOfWork,
        restaurants,
        scheduleSlots,
        deliveryWindows,
        scheduledSlotConfig,
        clock,
      }: Cradle) =>
        new ReserveScheduledSlotHandler({
          accessControl: slotReservationAccessControl,
          unitOfWork,
          restaurants,
          slots: scheduleSlots,
          windows: deliveryWindows,
          holdTtlMs: scheduledSlotConfig.holdTtlMs,
          alternatives: scheduledSlotConfig.alternatives,
          clock,
        }),
    ).singleton(),
    confirmScheduledSlot: asFunction(
      ({
        slotReservationAccessControl,
        unitOfWork,
        restaurants,
        scheduleSlots,
        clock,
      }: Cradle) =>
        new ConfirmScheduledSlotHandler({
          accessControl: slotReservationAccessControl,
          unitOfWork,
          restaurants,
          slots: scheduleSlots,
          clock,
        }),
    ).singleton(),
    releaseScheduledSlot: asFunction(
      ({
        slotReservationAccessControl,
        unitOfWork,
        scheduleSlots,
        clock,
      }: Cradle) =>
        new ReleaseScheduledSlotHandler({
          accessControl: slotReservationAccessControl,
          unitOfWork,
          slots: scheduleSlots,
          clock,
        }),
    ).singleton(),
  };
  container.register(registrations);
};
