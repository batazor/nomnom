import type { Db } from "mongodb";

import { ensureCollection } from "../../../../../infrastructure/repository/run-migrations.ts";

/**
 * Slice `scheduleslot` (change `add-scheduled-orders`): aggregate
 * `ScheduleSlot`, one document per Delivery Window of a restaurant with its
 * Slot Reservations (local ADR 2). Indexes: the windows of a restaurant by
 * start (List Scheduled Slots), the window of an order (Confirm, Release and
 * the repeat of Reserve; unique, so an order holds at most one window), the
 * purged holds of an order, and the TTL of passed windows.
 */
export async function up(db: Db): Promise<void> {
  await ensureCollection(db, {
    name: "schedule_slots",
    validator: {
      $jsonSchema: {
        bsonType: "object",
        required: [
          "_id",
          "version",
          "schemaVersion",
          "restaurantId",
          "windowStart",
          "windowEnd",
          "reservations",
          "lapsed",
        ],
        properties: {
          _id: { bsonType: "string", minLength: 1 },
          version: { bsonType: ["int", "long"], minimum: 1 },
          schemaVersion: { bsonType: "int", minimum: 1 },
          restaurantId: { bsonType: "string", minLength: 1 },
          windowStart: { bsonType: "date" },
          windowEnd: { bsonType: "date" },
          reservations: {
            bsonType: "array",
            items: {
              bsonType: "object",
              required: ["orderId", "status"],
              oneOf: [
                {
                  properties: { status: { enum: ["held"] } },
                  required: ["expiresAt"],
                },
                {
                  properties: { status: { enum: ["confirmed"] } },
                  not: { required: ["expiresAt"] },
                },
              ],
              properties: {
                orderId: { bsonType: "string", minLength: 1 },
                status: { enum: ["held", "confirmed"] },
                expiresAt: { bsonType: "date" },
              },
            },
          },
          lapsed: {
            bsonType: "array",
            items: { bsonType: "string", minLength: 1 },
          },
        },
      },
    },
    indexes: [
      {
        key: { restaurantId: 1, windowStart: 1 },
        name: "schedule_slots_restaurant_window",
      },
      {
        key: { "reservations.orderId": 1 },
        name: "schedule_slots_order_unique",
        unique: true,
        partialFilterExpression: {
          "reservations.orderId": { $exists: true },
        },
      },
      { key: { lapsed: 1 }, name: "schedule_slots_lapsed" },
      {
        key: { windowEnd: 1 },
        name: "schedule_slots_passed_ttl",
        expireAfterSeconds: 0,
      },
    ],
  });
}
