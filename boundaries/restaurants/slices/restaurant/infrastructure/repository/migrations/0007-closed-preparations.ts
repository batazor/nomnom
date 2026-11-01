import type { Db } from "mongodb";

import { ensureCollection } from "../../../../../infrastructure/repository/run-migrations.ts";

/**
 * Restaurant Orders that left the Wait Time Statistics: a redriven `queued`
 * fact must not reopen them. Records live as long as a dead letter can wait for
 * its redrive (the DLQ retention of ADR 28).
 */
export async function up(db: Db): Promise<void> {
  await ensureCollection(db, {
    name: "closed_preparations",
    validator: {
      $jsonSchema: {
        bsonType: "object",
        required: ["_id", "closedAt"],
        properties: {
          _id: { bsonType: "string", minLength: 1 },
          closedAt: { bsonType: "date" },
        },
      },
    },
    indexes: [
      {
        key: { closedAt: 1 },
        name: "closed_preparations_closed_at_ttl",
        expireAfterSeconds: 30 * 24 * 60 * 60,
      },
    ],
  });
}
