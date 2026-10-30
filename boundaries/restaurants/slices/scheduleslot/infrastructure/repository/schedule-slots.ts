import type { Db } from "mongodb";
import type { DatabaseUnitOfWork } from "../../../../infrastructure/repository/unit-of-work.ts";
import {
  VersionedCollection,
  type VersionedDocument,
} from "../../../../infrastructure/repository/versioned-collection.ts";
import { ConcurrencyConflictError } from "../../../../pkg/usecase/errors.ts";
import {
  ScheduleSlot,
  type ScheduleSlotRepository,
  scheduleSlotId,
} from "../../domains/scheduleslot/scheduleslot.ts";

export const SCHEDULE_SLOTS_COLLECTION = "schedule_slots";
export const SCHEDULE_SLOT_SCHEMA_VERSION = 1;

/**
 * One document per Delivery Window of a restaurant with its Slot
 * Reservations embedded (bounded by the capacity). `_id` is
 * `<restaurantId>|<windowStart ISO>`; `reservations.orderId` is unique across
 * windows, so an order holds at most one window. `windowEnd` drives the TTL
 * index: windows that already passed are removed by MongoDB.
 */
export type ScheduleSlotDocument = VersionedDocument & {
  restaurantId: string;
  windowStart: Date;
  windowEnd: Date;
  reservations: (
    | { orderId: string; status: "held"; expiresAt: Date }
    | { orderId: string; status: "confirmed" }
  )[];
  lapsed: string[];
};

const toSlot = (document: ScheduleSlotDocument): ScheduleSlot =>
  ScheduleSlot.restore(
    {
      restaurantId: document.restaurantId,
      window: { start: document.windowStart, end: document.windowEnd },
      reservations: document.reservations.map((reservation) => ({
        ...reservation,
      })),
      lapsed: [...document.lapsed],
    },
    document.version,
  );

export const createScheduleSlotCollection = (
  db: Db,
  unitOfWork: DatabaseUnitOfWork,
): VersionedCollection<ScheduleSlotDocument> =>
  new VersionedCollection<ScheduleSlotDocument>({
    db,
    name: SCHEDULE_SLOTS_COLLECTION,
    aggregateType: "ScheduleSlot",
    unitOfWork,
    // An order reserved concurrently in another window loses like a
    // concurrent write: the retry finds its reservation.
    duplicateKey: (keyPattern, document) =>
      "reservations.orderId" in keyPattern
        ? new ConcurrencyConflictError("ScheduleSlot", document._id)
        : undefined,
  });

export class DatabaseScheduleSlotRepository implements ScheduleSlotRepository {
  readonly #collection: VersionedCollection<ScheduleSlotDocument>;

  constructor(collection: VersionedCollection<ScheduleSlotDocument>) {
    this.#collection = collection;
  }

  async get(
    restaurantId: string,
    start: Date,
  ): Promise<ScheduleSlot | undefined> {
    const document = await this.#collection.findOne({
      _id: scheduleSlotId(restaurantId, start),
    });
    return document === undefined ? undefined : toSlot(document);
  }

  async findByOrderId(orderId: string): Promise<ScheduleSlot | undefined> {
    const document = await this.#collection.findOne({
      $or: [{ "reservations.orderId": orderId }, { lapsed: orderId }],
    });
    return document === undefined ? undefined : toSlot(document);
  }

  async listBetween(
    restaurantId: string,
    from: Date,
    to: Date,
  ): Promise<readonly ScheduleSlot[]> {
    const documents = await this.#collection.find(
      { restaurantId, windowStart: { $gte: from, $lte: to } },
      { sort: { windowStart: 1 } },
    );
    return documents.map(toSlot);
  }

  save(slot: ScheduleSlot): Promise<number> {
    const snapshot = slot.toSnapshot();
    return this.#collection.save(slot.id, slot.version, {
      schemaVersion: SCHEDULE_SLOT_SCHEMA_VERSION,
      restaurantId: snapshot.restaurantId,
      windowStart: snapshot.window.start,
      windowEnd: snapshot.window.end,
      reservations: snapshot.reservations.map((reservation) => ({
        ...reservation,
      })),
      lapsed: [...snapshot.lapsed],
    });
  }
}
