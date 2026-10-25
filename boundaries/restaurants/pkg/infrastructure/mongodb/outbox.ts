import { createHash } from "node:crypto";

import { Binary, type Collection, type Db } from "mongodb";

import type {
  AggregateRef,
  Outbox,
  PublishableEvent,
} from "../../application/outbox.ts";
import type { CollectionSpec } from "./migrate.ts";
import type { MongoUnitOfWork } from "./unit-of-work.ts";

export const OUTBOX_COLLECTION = "outbox";
export const OUTBOX_COUNTERS_COLLECTION = "outbox_counters";
export const OUTBOX_LEASES_COLLECTION = "outbox_leases";
export const OUTBOX_SCHEMA_VERSION = 1;
const SEQ_COUNTER_ID = "outbox_seq";

/** UUIDv5 namespace of Outbox event ids (ADR 31). */
export const OUTBOX_EVENT_ID_NAMESPACE = "8f1d3c2a-5b7e-4a90-9c61-2e4f7b8d0a13";

/**
 * Integration Event produced from a Domain Event by an
 * {@link OutboxEventMapper}: full Protobuf message name, Kafka topic and the
 * serialized Protobuf contract.
 */
export type OutboxIntegrationEvent = Readonly<{
  eventType: string;
  topic: string;
  payload: Uint8Array;
}>;

/**
 * Maps a Domain Event to its Integration Event; `undefined` means the event
 * is not published and no record is written.
 */
export type OutboxEventMapper = (
  aggregate: AggregateRef,
  event: PublishableEvent,
) => OutboxIntegrationEvent | undefined;

/** Default mapper until Protobuf contracts exist: nothing is published. */
export const noIntegrationEvents: OutboxEventMapper = () => undefined;

/** Persisted Outbox record (ADR 13, ADR 31). */
export type OutboxDocument = {
  _id: string;
  /** Monotonic insertion order from `outbox_counters`, same transaction. */
  seq: number;
  schemaVersion: number;
  /** UUIDv5 of `aggregateType:aggregateId:aggregateVersion:eventType`. */
  eventId: string;
  eventType: string;
  aggregateType: string;
  aggregateId: string;
  aggregateVersion: number;
  occurredAt: Date;
  topic: string;
  payload: Binary;
  metadata: { traceparent?: string; causationId?: string };
  publishedAt: Date | null;
  attempts: number;
  lastError?: string;
};

/** Lease of the single active relay instance (ADR 31). */
export type OutboxLeaseDocument = {
  _id: string;
  owner: string;
  expiresAt: Date;
};

export class MongoOutboxOutsideUnitOfWorkError extends Error {
  readonly code = "outbox_outside_unit_of_work";

  constructor() {
    super("outbox records can only be added inside an active unit of work");
    this.name = "MongoOutboxOutsideUnitOfWorkError";
  }
}

/** RFC 4122 UUIDv5 (SHA-1, name-based). */
export function uuidV5(name: string, namespace: string): string {
  const bytes = createHash("sha1")
    .update(Buffer.from(namespace.replaceAll("-", ""), "hex"))
    .update(name, "utf8")
    .digest()
    .subarray(0, 16);
  bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x50;
  bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
  const hex = bytes.toString("hex");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

export const outboxEventId = (
  aggregate: AggregateRef,
  eventType: string,
): string =>
  uuidV5(
    `${aggregate.type}:${aggregate.id}:${aggregate.version}:${eventType}`,
    OUTBOX_EVENT_ID_NAMESPACE,
  );

/**
 * Transactional Outbox in the same database: records and the `seq` counter
 * are written with the session of the active {@link MongoUnitOfWork}, so they
 * commit or abort together with the aggregate. Events the mapper skips are
 * not written. A duplicate `eventId` aborts the transaction.
 */
export class MongoOutbox implements Outbox {
  readonly #collection: Collection<OutboxDocument>;
  readonly #counters: Collection<{ _id: string; value: number }>;
  readonly #unitOfWork: MongoUnitOfWork;
  readonly #mapper: OutboxEventMapper;
  readonly #metadata: () => OutboxDocument["metadata"];

  constructor(
    db: Db,
    unitOfWork: MongoUnitOfWork,
    options: {
      mapper?: OutboxEventMapper;
      /** Trace context of the current operation (traceparent, causationId). */
      metadata?: () => OutboxDocument["metadata"];
    } = {},
  ) {
    this.#collection = db.collection<OutboxDocument>(OUTBOX_COLLECTION);
    this.#counters = db.collection(OUTBOX_COUNTERS_COLLECTION);
    this.#unitOfWork = unitOfWork;
    this.#mapper = options.mapper ?? noIntegrationEvents;
    this.#metadata = options.metadata ?? (() => ({}));
  }

  async add(
    aggregate: AggregateRef,
    events: readonly PublishableEvent[],
  ): Promise<void> {
    const session = this.#unitOfWork.session;
    if (session === undefined) {
      throw new MongoOutboxOutsideUnitOfWorkError();
    }
    const mapped = events.flatMap((event) => {
      const integration = this.#mapper(aggregate, event);
      return integration === undefined ? [] : [{ event, integration }];
    });
    if (mapped.length === 0) return;
    const counter = await this.#counters.findOneAndUpdate(
      { _id: SEQ_COUNTER_ID },
      { $inc: { value: mapped.length } },
      { upsert: true, returnDocument: "after", session },
    );
    const last = counter?.value ?? mapped.length;
    const first = last - mapped.length + 1;
    const metadata = this.#metadata();
    await this.#collection.insertMany(
      mapped.map(({ event, integration }, index) => {
        const eventId = outboxEventId(aggregate, integration.eventType);
        return {
          _id: eventId,
          seq: first + index,
          schemaVersion: OUTBOX_SCHEMA_VERSION,
          eventId,
          eventType: integration.eventType,
          aggregateType: aggregate.type,
          aggregateId: aggregate.id,
          aggregateVersion: aggregate.version,
          occurredAt: event.occurredAt,
          topic: integration.topic,
          payload: new Binary(integration.payload),
          metadata,
          publishedAt: null,
          attempts: 0,
        };
      }),
      { session },
    );
  }
}

export const outboxCollectionSpec: CollectionSpec = {
  name: OUTBOX_COLLECTION,
  validator: {
    $jsonSchema: {
      bsonType: "object",
      required: [
        "_id",
        "seq",
        "schemaVersion",
        "eventId",
        "eventType",
        "aggregateType",
        "aggregateId",
        "aggregateVersion",
        "occurredAt",
        "topic",
        "payload",
        "metadata",
        "publishedAt",
        "attempts",
      ],
      properties: {
        _id: { bsonType: "string" },
        seq: { bsonType: ["int", "long"], minimum: 1 },
        schemaVersion: { bsonType: "int", minimum: 1 },
        eventId: {
          bsonType: "string",
          pattern:
            "^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$",
        },
        eventType: { bsonType: "string", minLength: 1 },
        aggregateType: { bsonType: "string", minLength: 1 },
        aggregateId: { bsonType: "string", minLength: 1 },
        aggregateVersion: { bsonType: ["int", "long"], minimum: 1 },
        occurredAt: { bsonType: "date" },
        topic: { bsonType: "string", minLength: 1 },
        payload: { bsonType: "binData" },
        metadata: {
          bsonType: "object",
          properties: {
            traceparent: { bsonType: "string" },
            causationId: { bsonType: "string" },
          },
        },
        publishedAt: { bsonType: ["date", "null"] },
        attempts: { bsonType: ["int", "long"], minimum: 0 },
        lastError: { bsonType: "string" },
      },
    },
  },
  indexes: [
    { key: { eventId: 1 }, name: "outbox_event_id_unique", unique: true },
    { key: { seq: 1 }, name: "outbox_seq_unique", unique: true },
    // Relay: `{ publishedAt: null }` sorted by seq.
    { key: { publishedAt: 1, seq: 1 }, name: "outbox_pending_by_seq" },
  ],
};

export const outboxCountersCollectionSpec: CollectionSpec = {
  name: OUTBOX_COUNTERS_COLLECTION,
  validator: {
    $jsonSchema: {
      bsonType: "object",
      required: ["_id", "value"],
      properties: {
        _id: { bsonType: "string" },
        value: { bsonType: ["int", "long"], minimum: 0 },
      },
    },
  },
  indexes: [],
};

export const outboxLeasesCollectionSpec: CollectionSpec = {
  name: OUTBOX_LEASES_COLLECTION,
  validator: {
    $jsonSchema: {
      bsonType: "object",
      required: ["_id", "owner", "expiresAt"],
      properties: {
        _id: { bsonType: "string", minLength: 1 },
        owner: { bsonType: "string", minLength: 1 },
        expiresAt: { bsonType: "date" },
      },
    },
  },
  indexes: [{ key: { expiresAt: 1 }, name: "outbox_leases_expires" }],
};
