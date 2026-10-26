import type { Db } from "mongodb";

import { ensureCollection } from "../migrate.ts";

/** Transactional Outbox in the CDC contract (ADR 31, ADR 37). */
export async function up(db: Db): Promise<void> {
  await ensureCollection(db, {
    name: "outbox",
    validator: {
      $jsonSchema: {
        bsonType: "object",
        required: [
          "_id",
          "schemaVersion",
          "eventId",
          "eventType",
          "aggregateType",
          "aggregateId",
          "aggregateVersion",
          "occurredAt",
          "topic",
          "payload",
          "traceparent",
          "createdAt",
        ],
        properties: {
          _id: { bsonType: "string" },
          schemaVersion: { bsonType: "int", minimum: 1 },
          eventId: {
            bsonType: "string",
            pattern:
              "^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$",
          },
          eventType: { bsonType: "string", minLength: 1 },
          aggregateType: { bsonType: "string", minLength: 1 },
          aggregateId: { bsonType: "string", minLength: 1 },
          aggregateVersion: { bsonType: "long", minimum: 1 },
          occurredAt: { bsonType: "date" },
          topic: { bsonType: "string", minLength: 1 },
          payload: { bsonType: "binData" },
          traceparent: { bsonType: "string" },
          createdAt: { bsonType: "date" },
        },
      },
    },
    indexes: [
      { key: { eventId: 1 }, name: "outbox_event_id_unique", unique: true },
      // Retention (ADR 37): Debezium reads a record from the change stream
      // right after commit, so MongoDB removes records older than an hour.
      {
        key: { createdAt: 1 },
        name: "outbox_created_at_ttl",
        expireAfterSeconds: 60 * 60,
      },
    ],
  });
}
