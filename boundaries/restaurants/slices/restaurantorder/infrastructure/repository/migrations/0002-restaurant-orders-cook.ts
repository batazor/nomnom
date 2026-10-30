import type { Db } from "mongodb";

import { ensureCollection } from "../../../../../infrastructure/repository/run-migrations.ts";

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

/**
 * Slice `restaurantorder`, schemaVersion 2 (change `add-cook-and-courier-pools`,
 * local ADR 2): `cookId` of the cook assigned by Start Preparation, required
 * in Active Preparation, and one active order per cook.
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
    indexes: [
      {
        key: { cookId: 1 },
        name: "restaurant_orders_cook_active_unique",
        unique: true,
        partialFilterExpression: { state: { $in: activePreparation } },
      },
    ],
  });
}
