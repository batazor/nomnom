import type { Db } from "mongodb";

import { ensureCollection } from "../../../../../infrastructure/repository/run-migrations.ts";

/** Time zone of the launch Market: every restaurant stored before is in `berlin`. */
const LAUNCH_MARKET = "berlin";
const LAUNCH_TIME_ZONE = "Europe/Berlin";
const MARKET_ID = { bsonType: "string", pattern: "^[a-z][a-z0-9-]{1,31}$" };
const WAIT_LEVEL = { enum: ["NORMAL", "ELEVATED"] };
const TIME = { bsonType: "string", pattern: "^([01]\\d|2[0-3]):[0-5]\\d$" };

/**
 * Slice `restaurant`, schemaVersion 4 (change `add-restaurant-opening-hours`):
 * every Restaurant has an immutable IANA time zone and a weekly schedule.
 * Restaurants of the launch Market get `Europe/Berlin` explicitly; a stored
 * restaurant of another Market has no known zone, so the migration stops
 * instead of guessing. Stored schedules start empty: an empty schedule
 * accepts no orders, the kind seed sets the demo schedules. Idempotent.
 */
export async function up(db: Db): Promise<void> {
  const restaurants = db.collection("restaurants");
  const unknown = await restaurants.countDocuments({
    timeZone: { $exists: false },
    marketId: { $ne: LAUNCH_MARKET },
  });
  if (unknown > 0) {
    throw new Error(
      `${unknown} restaurants outside Market ${LAUNCH_MARKET} have no time zone`,
    );
  }
  await restaurants.updateMany(
    { timeZone: { $exists: false } },
    { $set: { timeZone: LAUNCH_TIME_ZONE } },
  );
  await restaurants.updateMany(
    { openingHours: { $exists: false } },
    { $set: { openingHours: [] } },
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
          "timeZone",
          "openingHours",
          "isOpen",
        ],
        properties: {
          _id: { bsonType: "string", minLength: 1 },
          version: { bsonType: ["int", "long"], minimum: 1 },
          schemaVersion: { bsonType: "int", minimum: 1 },
          name: { bsonType: "string", minLength: 1 },
          ownerId: { bsonType: "string", minLength: 1 },
          marketId: MARKET_ID,
          timeZone: { bsonType: "string", minLength: 1 },
          openingHours: {
            bsonType: "array",
            items: {
              bsonType: "object",
              required: ["day", "opens", "closes"],
              properties: {
                day: {
                  bsonType: ["int", "long", "double"],
                  minimum: 1,
                  maximum: 7,
                },
                opens: TIME,
                closes: TIME,
              },
            },
          },
          isOpen: { bsonType: "bool" },
          staff: {
            bsonType: "array",
            uniqueItems: true,
            items: { bsonType: "string", minLength: 1 },
          },
          authorizationRevision: { bsonType: "string", minLength: 1 },
          premiumFee: {
            bsonType: "object",
            required: ["amount", "currency"],
            properties: {
              amount: { bsonType: "string", pattern: "^\\d+(\\.\\d+)?$" },
              currency: { bsonType: "string", pattern: "^[A-Z]{3}$" },
            },
          },
          waitTime: {
            bsonType: "object",
            required: ["completed", "open", "level", "revision"],
            properties: {
              completed: {
                bsonType: "array",
                items: {
                  bsonType: "object",
                  required: ["orderId", "readyAt", "durationMs"],
                  properties: {
                    orderId: { bsonType: "string", minLength: 1 },
                    readyAt: { bsonType: "date" },
                    durationMs: {
                      bsonType: ["int", "long", "double"],
                      minimum: 0,
                    },
                  },
                },
              },
              open: {
                bsonType: "array",
                items: {
                  bsonType: "object",
                  required: ["orderId", "acceptedAt"],
                  properties: {
                    orderId: { bsonType: "string", minLength: 1 },
                    acceptedAt: { bsonType: "date" },
                  },
                },
              },
              level: WAIT_LEVEL,
              revision: { bsonType: ["int", "long"], minimum: 0 },
            },
          },
        },
      },
    },
    indexes: [],
  });
}
