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
  "PICKED_UP",
  "REJECTED",
  "CANCELLED",
  "FAILED",
];
/** Active Preparation: the cook holds the order. */
const activePreparation = ["PREPARING", "ASSEMBLED"];
const optionalDate = { bsonType: "date" };

/**
 * Slice `restaurantorder`, schemaVersion 6 (FR-683): a scheduled order
 * carries its Delivery Window and Planned Ready At as `schedule`, complete
 * or absent. Stored orders have no window and stay valid. The scheduled
 * column of the kitchen board reads accepted scheduled orders by Planned
 * Ready At. Idempotent.
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
          schedule: {
            bsonType: "object",
            required: ["windowStart", "windowEnd", "plannedReadyAt"],
            properties: {
              windowStart: { bsonType: "date" },
              windowEnd: { bsonType: "date" },
              plannedReadyAt: { bsonType: "date" },
            },
          },
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
      // List Scheduled Orders: accepted scheduled orders by Planned Ready At.
      {
        key: { restaurantId: 1, state: 1, "schedule.plannedReadyAt": 1 },
        name: "restaurant_orders_board_scheduled",
        partialFilterExpression: { schedule: { $exists: true } },
      },
    ],
  });
}
