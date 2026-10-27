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
  "PICKED_UP",
  "REJECTED",
  "CANCELLED",
  "FAILED",
];
/** Active Preparation: the cook holds the order. */
const activePreparation = ["PREPARING", "ASSEMBLED"];
const optionalDate = { bsonType: "date" };

/**
 * Slice `restaurantorder`, schemaVersion 5 (FR-651, FR-225): every order has
 * its Premium Priority. Orders received before Premium Priority existed were
 * standard, so they get `STANDARD` explicitly, then the validator requires
 * `priority`. `startRequestId` is the request of Start Next Preparation that
 * started the order; one request starts at most one order of a restaurant.
 * Idempotent: a repeat finds nothing to set and the index already exists.
 */
export async function up(db: Db): Promise<void> {
  await db
    .collection("restaurant_orders")
    .updateMany(
      { priority: { $exists: false } },
      { $set: { priority: "STANDARD" } },
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
          "priority",
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
              pickedUpAt: optionalDate,
            },
          },
          priority: { enum: ["STANDARD", "PREMIUM"] },
          cookId: { bsonType: "string", minLength: 1 },
          startRequestId: { bsonType: "string", minLength: 1 },
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
    indexes: [
      // A repeated Start Next Preparation finds the order its request started.
      {
        key: { restaurantId: 1, startRequestId: 1 },
        name: "restaurant_orders_start_request_unique",
        unique: true,
        partialFilterExpression: { startRequestId: { $exists: true } },
      },
    ],
  });
}
