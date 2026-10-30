import type { Db } from "mongodb";

import { ensureCollection } from "../../../../../infrastructure/repository/run-migrations.ts";

/** Launch Market of the data created before Markets existed (ADR 41). */
const LAUNCH_MARKET = "berlin";
const MARKET_ID = { bsonType: "string", pattern: "^[a-z][a-z0-9-]{1,31}$" };

/**
 * Slice `restaurant`, schemaVersion 2 (ADR 41): every Restaurant belongs to
 * one immutable Market. Restaurants stored before get the launch Market
 * `berlin` explicitly, then the validator requires `marketId`. The catalog
 * and owned lists are paged by id within one Market, so their indexes lead
 * with the Market and replace the indexes without it. Idempotent: a repeat
 * finds nothing to set and the indexes already exist.
 */
export async function up(db: Db): Promise<void> {
  const restaurants = db.collection("restaurants");
  await restaurants.updateMany(
    { marketId: { $exists: false } },
    { $set: { marketId: LAUNCH_MARKET } },
  );
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
          "marketId",
          "isOpen",
        ],
        properties: {
          _id: { bsonType: "string", minLength: 1 },
          version: { bsonType: ["int", "long"], minimum: 1 },
          schemaVersion: { bsonType: "int", minimum: 1 },
          name: { bsonType: "string", minLength: 1 },
          ownerId: { bsonType: "string", minLength: 1 },
          marketId: MARKET_ID,
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
    indexes: [
      // List Restaurants of one Market: open restaurants paged by id.
      {
        key: { marketId: 1, isOpen: 1, _id: 1 },
        name: "restaurants_market_open_by_id",
      },
      // List Restaurants with include_closed: every restaurant of the Market.
      { key: { marketId: 1, _id: 1 }, name: "restaurants_market_by_id" },
      // List Owned Restaurants: the owner's restaurants of one Market.
      {
        key: { ownerId: 1, marketId: 1, _id: 1 },
        name: "restaurants_owner_market_by_id",
      },
    ],
  });
  for (const name of ["restaurants_open_by_id", "restaurants_owner"]) {
    await dropIndexIfExists(db, "restaurants", name);
  }
}

/** IndexNotFound: dropped by an earlier run of this migration. */
async function dropIndexIfExists(
  db: Db,
  collection: string,
  name: string,
): Promise<void> {
  try {
    await db.collection(collection).dropIndex(name);
  } catch (error) {
    if (
      !(
        typeof error === "object" &&
        error !== null &&
        "code" in error &&
        error.code === 27
      )
    ) {
      throw error;
    }
  }
}
