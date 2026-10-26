import type { Db } from "mongodb";

import { ensureCollection } from "../../../../../pkg/infrastructure/repository/migrate.ts";

/** Slice `cook`: aggregate `Cook` of the Cook Pool (local ADR 2). */
export async function up(db: Db): Promise<void> {
  await ensureCollection(db, {
    name: "cooks",
    validator: {
      $jsonSchema: {
        bsonType: "object",
        required: [
          "_id",
          "version",
          "schemaVersion",
          "restaurantId",
          "name",
          "onLine",
        ],
        properties: {
          _id: { bsonType: "string", minLength: 1 },
          version: { bsonType: ["int", "long"], minimum: 1 },
          schemaVersion: { bsonType: "int", minimum: 1 },
          restaurantId: { bsonType: "string", minLength: 1 },
          name: { bsonType: "string", minLength: 1, maxLength: 64 },
          onLine: { bsonType: "bool" },
          lastAssignedAt: { bsonType: "date" },
          retiredAt: { bsonType: "date" },
        },
      },
    },
    indexes: [
      // Cook Pool of a restaurant and the choice of a free cook.
      {
        key: { restaurantId: 1, retiredAt: 1, onLine: 1, lastAssignedAt: 1 },
        name: "cooks_pool",
      },
    ],
  });
}
