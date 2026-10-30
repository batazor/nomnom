import type { Db } from "mongodb";

import { ensureCollection } from "../../../../../infrastructure/repository/run-migrations.ts";

/**
 * Slice `restaurant`: the pickup address is dropped from the project, like the
 * delivery address before it. The validator no longer knows `address`, and
 * stored documents lose the field. Idempotent: a repeat finds nothing to unset.
 */
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
          staff: {
            bsonType: "array",
            uniqueItems: true,
            items: { bsonType: "string", minLength: 1 },
          },
          authorizationRevision: { bsonType: "string", minLength: 1 },
        },
      },
    },
    indexes: [],
  });
  await db
    .collection("restaurants")
    .updateMany({ address: { $exists: true } }, { $unset: { address: "" } });
}
