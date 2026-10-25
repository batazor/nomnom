import { createHash } from "node:crypto";

import { Binary, type Collection, type Db, Long } from "mongodb";

import type {
  AggregateRef,
  ChangeNotification,
  ChangeNotifications,
  Outbox,
  PublishableEvent,
} from "../../application/outbox.ts";
import type { MongoUnitOfWork } from "./unit-of-work.ts";

export const OUTBOX_COLLECTION = "outbox";
export const OUTBOX_SCHEMA_VERSION = 2;

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

/**
 * Persisted Outbox record (ADR 13, ADR 31) in the CDC contract of ADR 37:
 * Debezium MongoEventRouter builds the Kafka message from these fields only
 * (key `aggregateId`, topic `topic`, value `payload`, the other contract
 * fields as Event Envelope headers). A document without one of the header
 * fields stops the connector, so every field is always present.
 */
export type OutboxDocument = {
  _id: string;
  schemaVersion: number;
  /** UUIDv5 of `aggregateType:aggregateId:aggregateVersion:eventType`. */
  eventId: string;
  eventType: string;
  aggregateType: string;
  aggregateId: string;
  aggregateVersion: Long;
  occurredAt: Date;
  topic: string;
  payload: Binary;
  /** W3C trace context of the writing call; `""` without one. */
  traceparent: string;
  /** Write time; the TTL index removes the record an hour later. */
  createdAt: Date;
};

/** Trace context of the operation that writes the records. */
export type OutboxTraceContext = { traceparent?: string };

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
 * Transactional Outbox in the same database: records are written with the
 * session of the active {@link MongoUnitOfWork}, so they commit or abort
 * together with the aggregate; Debezium publishes them from the change stream
 * in commit order (ADR 37). Events the mapper skips are not written. A
 * duplicate `eventId` aborts the transaction.
 */
export class MongoOutbox implements Outbox, ChangeNotifications {
  readonly #collection: Collection<OutboxDocument>;
  readonly #unitOfWork: MongoUnitOfWork;
  readonly #mapper: OutboxEventMapper;
  readonly #traceContext: () => OutboxTraceContext;

  constructor(
    db: Db,
    unitOfWork: MongoUnitOfWork,
    options: {
      mapper?: OutboxEventMapper;
      /** Trace context of the current operation (traceparent). */
      metadata?: () => OutboxTraceContext;
    } = {},
  ) {
    this.#collection = db.collection<OutboxDocument>(OUTBOX_COLLECTION);
    this.#unitOfWork = unitOfWork;
    this.#mapper = options.mapper ?? noIntegrationEvents;
    this.#traceContext = options.metadata ?? (() => ({}));
  }

  async add(
    aggregate: AggregateRef,
    events: readonly PublishableEvent[],
  ): Promise<void> {
    if (this.#unitOfWork.session === undefined) {
      throw new MongoOutboxOutsideUnitOfWorkError();
    }
    const mapped = events.flatMap((event) => {
      const integration = this.#mapper(aggregate, event);
      return integration === undefined
        ? []
        : [{ occurredAt: event.occurredAt, integration }];
    });
    await this.#insert(aggregate, mapped);
  }

  /**
   * Change Notification (ADR 36) next to the change itself: a JSON entity key
   * for the subscriptions of the BFF Router, not an Integration Event.
   */
  async notify(
    aggregate: AggregateRef,
    notification: ChangeNotification,
  ): Promise<void> {
    await this.#insert(aggregate, [
      {
        occurredAt: notification.occurredAt,
        integration: {
          eventType: notification.type,
          topic: notification.topic,
          payload: new TextEncoder().encode(
            JSON.stringify(notification.entity),
          ),
        },
      },
    ]);
  }

  async #insert(
    aggregate: AggregateRef,
    mapped: readonly {
      occurredAt: Date;
      integration: OutboxIntegrationEvent;
    }[],
  ): Promise<void> {
    const session = this.#unitOfWork.session;
    if (session === undefined) {
      throw new MongoOutboxOutsideUnitOfWorkError();
    }
    if (mapped.length === 0) return;
    const traceparent = this.#traceContext().traceparent ?? "";
    const createdAt = new Date();
    await this.#collection.insertMany(
      mapped.map(({ occurredAt, integration }) => {
        const eventId = outboxEventId(aggregate, integration.eventType);
        return {
          _id: eventId,
          schemaVersion: OUTBOX_SCHEMA_VERSION,
          eventId,
          eventType: integration.eventType,
          aggregateType: aggregate.type,
          aggregateId: aggregate.id,
          aggregateVersion: Long.fromNumber(aggregate.version),
          occurredAt,
          topic: integration.topic,
          payload: new Binary(integration.payload),
          traceparent,
          createdAt,
        };
      }),
      { session },
    );
  }
}
