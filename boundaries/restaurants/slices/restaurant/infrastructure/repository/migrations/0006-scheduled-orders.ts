import type { Db } from "mongodb";

import { ensureCollection } from "../../../../../pkg/infrastructure/repository/migrate.ts";

const MARKET_ID = { bsonType: "string", pattern: "^[a-z][a-z0-9-]{1,31}$" };
const WAIT_LEVEL = { enum: ["NORMAL", "ELEVATED"] };
const TIME = { bsonType: "string", pattern: "^([01]\\d|2[0-3]):[0-5]\\d$" };

/**
 * Slice `restaurant`, schemaVersion 5 (change `add-scheduled-orders`):
 * - the optional settings of scheduled orders (FR-672) join the validator;
 *   stored restaurants have none and take no scheduled orders until the
 *   owner sets them;
 * - open orders of the Wait Time Statistics are measured from joining the
 *   Kitchen Queue (FR-644): `waitTime.open[].acceptedAt` becomes `queuedAt`.
 *   Every order opened before was queued in the Unit of Work of its
 *   Acceptance, so the stored moment is its queuing moment.
 * Idempotent: a repeat finds no `acceptedAt` to rename.
 */
export async function up(db: Db): Promise<void> {
  await db
    .collection("restaurants")
    .updateMany({ "waitTime.open.acceptedAt": { $exists: true } }, [
      {
        $set: {
          "waitTime.open": {
            $map: {
              input: "$waitTime.open",
              in: {
                orderId: "$$this.orderId",
                queuedAt: "$$this.acceptedAt",
              },
            },
          },
        },
      },
    ]);
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
          scheduledOrders: {
            bsonType: "object",
            required: ["slotCapacity", "openingLeadMs", "closingGraceMs"],
            properties: {
              slotCapacity: { bsonType: ["int", "long"], minimum: 1 },
              openingLeadMs: {
                bsonType: ["int", "long", "double"],
                minimum: 0,
              },
              closingGraceMs: {
                bsonType: ["int", "long", "double"],
                minimum: 0,
              },
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
                  required: ["orderId", "queuedAt"],
                  properties: {
                    orderId: { bsonType: "string", minLength: 1 },
                    queuedAt: { bsonType: "date" },
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
