import { type Collection, type Db, MongoServerError } from "mongodb";

import type { Inbox, InboxRecord } from "../messaging/processor.ts";
import type { DatabaseUnitOfWork } from "./unit-of-work.ts";

export const INBOX_COLLECTION = "inbox";
export const INBOX_SCHEMA_VERSION = 1;

/** Факт обработки одного Integration Event одним consumer (ADR 11). */
export type InboxDocument = {
  /** `{consumer}:{event_id}`. */
  _id: string;
  schemaVersion: number;
  consumer: string;
  eventId: string;
  eventType: string;
  aggregateId: string;
  topic: string;
  processedAt: Date;
};

/** Конфликт unique index Inbox: событие уже обработано параллельно. */
class InboxDuplicateError extends Error {
  constructor() {
    super("inbox record already exists");
    this.name = "InboxDuplicateError";
  }
}

/**
 * Transactional Inbox в базе restaurants: проверка `(consumer, event_id)`,
 * запись Inbox и изменения `work` выполняются в одной MongoDB-транзакции
 * {@link DatabaseUnitOfWork}; репозитории `work` присоединяются к ней через
 * async context. Конфликт unique index откатывает транзакцию и считается
 * duplicate. Конкурентная вставка того же `_id` в другой транзакции даёт
 * WriteConflict, и driver повторяет транзакцию — повтор видит запись.
 */
export class DatabaseInbox implements Inbox {
  readonly #collection: Collection<InboxDocument>;
  readonly #unitOfWork: DatabaseUnitOfWork;
  readonly #clock: () => Date;

  constructor(
    db: Db,
    unitOfWork: DatabaseUnitOfWork,
    clock: () => Date = () => new Date(),
  ) {
    this.#collection = db.collection<InboxDocument>(INBOX_COLLECTION);
    this.#unitOfWork = unitOfWork;
    this.#clock = clock;
  }

  async runOnce(
    record: InboxRecord,
    work: () => Promise<void>,
  ): Promise<"processed" | "duplicate"> {
    try {
      return await this.#unitOfWork.run(async () => {
        const session = this.#unitOfWork.session;
        const existing = await this.#collection.findOne(
          { consumer: record.consumer, eventId: record.eventId },
          { session, projection: { _id: 1 } },
        );
        if (existing !== null) return "duplicate";
        try {
          await this.#collection.insertOne(
            {
              _id: `${record.consumer}:${record.eventId}`,
              schemaVersion: INBOX_SCHEMA_VERSION,
              consumer: record.consumer,
              eventId: record.eventId,
              eventType: record.eventType,
              aggregateId: record.aggregateId,
              topic: record.topic,
              processedAt: this.#clock(),
            },
            { session },
          );
        } catch (error) {
          if (error instanceof MongoServerError && error.code === 11000) {
            throw new InboxDuplicateError();
          }
          throw error;
        }
        await work();
        return "processed" as const;
      });
    } catch (error) {
      if (error instanceof InboxDuplicateError) return "duplicate";
      throw error;
    }
  }
}
