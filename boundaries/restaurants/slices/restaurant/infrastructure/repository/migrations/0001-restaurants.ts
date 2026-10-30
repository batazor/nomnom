import type { Db } from "mongodb";

import { ensureCollection } from "../../../../../infrastructure/repository/run-migrations.ts";

/** Slice `restaurant`: aggregate `Restaurant` (local ADR 2). */
export async function up(db: Db): Promise<void> {
  await ensureCollection(db, {
    name: "restaurants",
    validator: {
      $jsonSchema: {
        bsonType: "object",
        required: [
          "_id",
          "version",
          "schemaVersion",
          "name",
          "ownerId",
          "isOpen",
        ],
        properties: {
          _id: { bsonType: "string", minLength: 1 },
          version: { bsonType: ["int", "long"], minimum: 1 },
          schemaVersion: { bsonType: "int", minimum: 1 },
          name: { bsonType: "string", minLength: 1 },
          ownerId: { bsonType: "string", minLength: 1 },
          isOpen: { bsonType: "bool" },
          address: { bsonType: "string" },
          staff: {
            bsonType: "array",
            uniqueItems: true,
            items: { bsonType: "string", minLength: 1 },
          },
          authorizationRevision: { bsonType: "string", minLength: 1 },
        },
      },
    },
    indexes: [
      // Public catalog: open restaurants paged by id.
      { key: { isOpen: 1, _id: 1 }, name: "restaurants_open_by_id" },
      { key: { ownerId: 1 }, name: "restaurants_owner" },
    ],
  });
}
