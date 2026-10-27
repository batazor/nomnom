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
 * Slice `restaurantorder`, schemaVersion 4: a `READY` order goes to
 * `PICKED_UP` when the courier takes it (DeliveryPickedUp of `delivery`), with
 * `timeline.pickedUpAt`. Only the validator grows; stored documents stay
 * valid. Indexes do not change. Idempotent.
 */
export async function up(db: Db): Promise<void> {
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
              pickedUpAt: optionalDate,
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
