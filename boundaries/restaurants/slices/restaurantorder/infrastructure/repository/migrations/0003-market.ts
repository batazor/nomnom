import type { Db } from "mongodb";

import { ensureCollection } from "../../../../../pkg/infrastructure/repository/migrate.ts";

/** States of the `RestaurantOrder` FSM as of this migration. */
const states = [
  "RECEIVED",
  "ACCEPTED",
  "QUEUED",
  "PREPARING",
  "ASSEMBLED",
  "READY",
  "REJECTED",
  "CANCELLED",
  "FAILED",
];
/** Active Preparation: the cook holds the order. */
const activePreparation = ["PREPARING", "ASSEMBLED"];
const optionalDate = { bsonType: "date" };

/** Launch Market of the data created before Markets existed (ADR 41). */
const LAUNCH_MARKET = "berlin";

/**
 * Slice `restaurantorder`, schemaVersion 3 (ADR 41): a Restaurant Order
 * belongs to the Market of the restaurant that received it. Orders stored
 * before get the launch Market `berlin` explicitly, then the validator
 * requires `marketId`. The kitchen board and the Simulator read by restaurant
 * or state, and a restaurant has one Market, so no index changes. Idempotent:
 * a repeat finds nothing to set.
 */
export async function up(db: Db): Promise<void> {
  await db
    .collection("restaurant_orders")
    .updateMany(
      { marketId: { $exists: false } },
      { $set: { marketId: LAUNCH_MARKET } },
    );
  await ensureCollection(db, {
    name: "restaurant_orders",
    validator: {
      $jsonSchema: {
        bsonType: "object",
        required: [
          "_id",
          "version",
          "schemaVersion",
          "orderId",
          "restaurantId",
          "marketId",
          "items",
          "state",
          "timeline",
        ],
        anyOf: [
          { properties: { state: { not: { enum: activePreparation } } } },
          { required: ["cookId"] },
        ],
        properties: {
          _id: { bsonType: "string", minLength: 1 },
          version: { bsonType: ["int", "long"], minimum: 1 },
          schemaVersion: { bsonType: "int", minimum: 1 },
          orderId: { bsonType: "string", minLength: 1 },
          restaurantId: { bsonType: "string", minLength: 1 },
          marketId: {
            bsonType: "string",
            pattern: "^[a-z][a-z0-9-]{1,31}$",
          },
          items: {
            bsonType: "array",
            minItems: 1,
            items: {
              bsonType: "object",
              required: ["menuItemId", "quantity", "options"],
              properties: {
                menuItemId: { bsonType: "string", minLength: 1 },
                quantity: { bsonType: ["int", "long"], minimum: 1 },
                options: { bsonType: "array", items: { bsonType: "string" } },
              },
            },
          },
          state: { enum: states },
          timeline: {
            bsonType: "object",
            properties: {
              receivedAt: optionalDate,
              acceptedAt: optionalDate,
              queuedAt: optionalDate,
              preparationStartedAt: optionalDate,
              expectedReadyAt: optionalDate,
              readyAt: optionalDate,
            },
          },
          cookId: { bsonType: "string", minLength: 1 },
          rejectionReason: { bsonType: "string" },
          failureReason: { bsonType: "string" },
          cancellation: {
            bsonType: "object",
            required: ["stage"],
            properties: {
              stage: { enum: states },
              reason: { bsonType: "string" },
            },
          },
        },
      },
    },
    indexes: [],
  });
}
