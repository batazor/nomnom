import type { Db } from "mongodb";

import { ensureCollection } from "../../../../../infrastructure/repository/run-migrations.ts";

/** Launch Market of the data created before Markets existed (ADR 41). */
const LAUNCH_MARKET = "berlin";

/**
 * Slice `menu`, schemaVersion 2 (ADR 41): a Menu carries the Market of its
 * restaurant, published with every menu Integration Event. Menus stored
 * before belong to restaurants of the launch Market and get `berlin`
 * explicitly, then the validator requires `marketId`. Menus are read by
 * restaurant only, so no index changes. Idempotent: a repeat finds nothing to
 * set.
 */
export async function up(db: Db): Promise<void> {
  await db
    .collection("menus")
    .updateMany(
      { marketId: { $exists: false } },
      { $set: { marketId: LAUNCH_MARKET } },
    );
  await ensureCollection(db, {
    name: "menus",
    validator: {
      $jsonSchema: {
        bsonType: "object",
        required: [
          "_id",
          "version",
          "schemaVersion",
          "restaurantId",
          "marketId",
          "items",
        ],
        properties: {
          _id: { bsonType: "string", minLength: 1 },
          version: { bsonType: ["int", "long"], minimum: 1 },
          schemaVersion: { bsonType: "int", minimum: 1 },
          restaurantId: { bsonType: "string", minLength: 1 },
          marketId: {
            bsonType: "string",
            pattern: "^[a-z][a-z0-9-]{1,31}$",
          },
          items: {
            bsonType: "array",
            items: {
              bsonType: "object",
              required: ["id", "name", "price", "isAvailable"],
              properties: {
                id: { bsonType: "string", minLength: 1 },
                name: { bsonType: "string", minLength: 1 },
                description: { bsonType: "string" },
                price: {
                  bsonType: "object",
                  required: ["amount", "currency"],
                  properties: {
                    amount: { bsonType: "string", pattern: "^\\d+(\\.\\d+)?$" },
                    currency: { bsonType: "string", pattern: "^[A-Z]{3}$" },
                  },
                },
                isAvailable: { bsonType: "bool" },
              },
            },
          },
        },
      },
    },
    indexes: [],
  });
}
