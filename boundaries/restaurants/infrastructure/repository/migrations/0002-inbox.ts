import type { Db } from "mongodb";

import { ensureCollection } from "../run-migrations.ts";

/** Transactional Inbox (ADR 11). */
export async function up(db: Db): Promise<void> {
  await ensureCollection(db, {
    name: "inbox",
    validator: {
      $jsonSchema: {
        bsonType: "object",
        required: [
          "_id",
          "schemaVersion",
          "consumer",
          "eventId",
          "eventType",
          "aggregateId",
          "topic",
          "processedAt",
        ],
        properties: {
          _id: { bsonType: "string", minLength: 1 },
          schemaVersion: { bsonType: "int", minimum: 1 },
          consumer: { bsonType: "string", minLength: 1 },
          eventId: { bsonType: "string", minLength: 1 },
          eventType: { bsonType: "string", minLength: 1 },
          aggregateId: { bsonType: "string", minLength: 1 },
          topic: { bsonType: "string", minLength: 1 },
          processedAt: { bsonType: "date" },
        },
      },
    },
    indexes: [
      {
        key: { consumer: 1, eventId: 1 },
        name: "inbox_consumer_event_unique",
        unique: true,
      },
      // Retention (ADR 11): 30 дней — больше retention Kafka по умолчанию
      // (7 дней) и окна, в котором consumer group может перечитать стрим.
      {
        key: { processedAt: 1 },
        name: "inbox_processed_at_ttl",
        expireAfterSeconds: 30 * 24 * 60 * 60,
      },
    ],
  });
}
