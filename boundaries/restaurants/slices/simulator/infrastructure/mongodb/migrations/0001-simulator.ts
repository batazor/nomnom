import type { Db } from "mongodb";

import { ensureCollection } from "../../../../../pkg/infrastructure/mongodb/migrate.ts";

/** Slice `simulator`: the single Demo Simulator state document (FR-604). */
export async function up(db: Db): Promise<void> {
  await ensureCollection(db, {
    name: "simulator",
    validator: {
      $jsonSchema: {
        bsonType: "object",
        required: ["_id", "schemaVersion", "enabled", "changedAt"],
        properties: {
          _id: { enum: ["kitchen"] },
          schemaVersion: { bsonType: "int", minimum: 1 },
          enabled: { bsonType: "bool" },
          changedAt: { bsonType: "date" },
        },
      },
    },
    indexes: [],
  });
}
