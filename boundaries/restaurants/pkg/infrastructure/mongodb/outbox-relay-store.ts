import { type Collection, type Db, MongoServerError } from "mongodb";

import type { PendingStats, RelayRecord, RelayStore } from "../outbox/relay.ts";
import {
  OUTBOX_COLLECTION,
  OUTBOX_LEASES_COLLECTION,
  type OutboxDocument,
  type OutboxLeaseDocument,
} from "./outbox.ts";

/** `_id` of the single relay lease of restaurants. */
export const RELAY_LEASE_ID = "restaurants-relay";

const DUPLICATE_KEY = 11000;

/**
 * Relay side of the MongoDB Outbox (ADR 31): the lease document in
 * `outbox_leases`, pending records by `{ publishedAt: null }` sorted by `seq`
 * (index `outbox_pending_by_seq`) and retention of published records.
 */
export class MongoOutboxRelayStore implements RelayStore {
  readonly #outbox: Collection<OutboxDocument>;
  readonly #leases: Collection<OutboxLeaseDocument>;
  readonly #leaseId: string;

  constructor(db: Db, options: { leaseId?: string } = {}) {
    this.#outbox = db.collection<OutboxDocument>(OUTBOX_COLLECTION);
    this.#leases = db.collection<OutboxLeaseDocument>(OUTBOX_LEASES_COLLECTION);
    this.#leaseId = options.leaseId ?? RELAY_LEASE_ID;
  }

  async acquireLease(
    owner: string,
    now: Date,
    ttlMs: number,
  ): Promise<boolean> {
    try {
      // Matches when this owner holds the lease or it expired; otherwise the
      // upsert collides with the existing `_id` and the lease stays foreign.
      await this.#leases.updateOne(
        {
          _id: this.#leaseId,
          $or: [{ owner }, { expiresAt: { $lte: now } }],
        },
        { $set: { owner, expiresAt: new Date(now.getTime() + ttlMs) } },
        { upsert: true },
      );
      return true;
    } catch (error) {
      if (error instanceof MongoServerError && error.code === DUPLICATE_KEY) {
        return false;
      }
      throw error;
    }
  }

  async releaseLease(owner: string): Promise<void> {
    await this.#leases.deleteOne({ _id: this.#leaseId, owner });
  }

  async fetchPending(limit: number): Promise<readonly RelayRecord[]> {
    const documents = await this.#outbox
      .find({ publishedAt: null })
      .sort({ seq: 1 })
      .limit(limit)
      .toArray();
    return documents.map((document) => ({
      id: document._id,
      seq: Number(document.seq),
      eventId: document.eventId,
      eventType: document.eventType,
      aggregateType: document.aggregateType,
      aggregateId: document.aggregateId,
      aggregateVersion: Number(document.aggregateVersion),
      occurredAt: document.occurredAt,
      topic: document.topic,
      payload: document.payload.value(),
      metadata: document.metadata ?? {},
      attempts: Number(document.attempts),
    }));
  }

  async markPublished(id: string, publishedAt: Date): Promise<void> {
    await this.#outbox.updateOne(
      { _id: id, publishedAt: null },
      { $set: { publishedAt }, $unset: { lastError: "" } },
    );
  }

  async markFailed(id: string, error: string): Promise<void> {
    await this.#outbox.updateOne(
      { _id: id, publishedAt: null },
      { $inc: { attempts: 1 }, $set: { lastError: error } },
    );
  }

  async deletePublishedBefore(cutoff: Date): Promise<number> {
    const result = await this.#outbox.deleteMany({
      publishedAt: { $ne: null, $lt: cutoff },
    });
    return result.deletedCount;
  }

  async pendingStats(): Promise<readonly PendingStats[]> {
    const rows = await this.#outbox
      .aggregate<{ _id: string; pending: number; oldest: Date | null }>([
        { $match: { publishedAt: null } },
        {
          $group: {
            _id: "$topic",
            pending: { $sum: 1 },
            oldest: { $min: "$occurredAt" },
          },
        },
      ])
      .toArray();
    return rows.map((row) => ({
      stream: row._id,
      pending: row.pending,
      oldestOccurredAt: row.oldest ?? undefined,
    }));
  }
}
