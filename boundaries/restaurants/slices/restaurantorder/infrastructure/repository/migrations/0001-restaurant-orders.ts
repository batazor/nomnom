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
const optionalDate = { bsonType: "date" };

/** Slice `restaurantorder`: aggregate `RestaurantOrder` (local ADR 2). */
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
        key: { orderId: 1 },
        name: "restaurant_orders_order_unique",
        unique: true,
      },
      // Kitchen board: incoming, queue, active preparations and ready orders
      // per restaurant.
      {
        key: { restaurantId: 1, state: 1, "timeline.receivedAt": 1 },
        name: "restaurant_orders_board_received",
      },
      {
        key: { restaurantId: 1, state: 1, "timeline.preparationStartedAt": 1 },
        name: "restaurant_orders_board_started",
      },
      {
        key: { restaurantId: 1, state: 1, "timeline.readyAt": 1 },
        name: "restaurant_orders_board_ready",
      },
      // Demo Simulator: orders of every restaurant waiting for the kitchen.
      {
        key: { state: 1, "timeline.receivedAt": 1 },
        name: "restaurant_orders_state_received",
      },
    ],
  });
}
