import type { Db } from "mongodb";

import { ensureCollection } from "../../../../../infrastructure/repository/run-migrations.ts";

const MARKET_ID = { bsonType: "string", pattern: "^[a-z][a-z0-9-]{1,31}$" };
const WAIT_LEVEL = { enum: ["NORMAL", "ELEVATED"] };

/**
 * Slice `restaurant`, schemaVersion 3: the optional fee of Premium Priority
 * (FR-202) and the Wait Time Statistics read model (FR-644) join the
 * validator. Stored documents stay valid: both fields are optional. Indexes
 * do not change. Idempotent.
 */
export async function up(db: Db): Promise<void> {
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
