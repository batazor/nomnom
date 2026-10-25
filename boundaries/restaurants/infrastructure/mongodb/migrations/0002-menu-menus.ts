import type { Db } from "mongodb";

import { ensureCollection } from "../../../pkg/infrastructure/mongodb/migrate.ts";

/** Slice `menu`: aggregate `Menu`, one per restaurant (local ADR 2). */
export async function up(db: Db): Promise<void> {
  await ensureCollection(db, {
    name: "menus",
    validator: {
      $jsonSchema: {
        bsonType: "object",
        required: ["_id", "version", "schemaVersion", "restaurantId", "items"],
        properties: {
          _id: { bsonType: "string", minLength: 1 },
          version: { bsonType: ["int", "long"], minimum: 1 },
          schemaVersion: { bsonType: "int", minimum: 1 },
          restaurantId: { bsonType: "string", minLength: 1 },
          items: {
            bsonType: "array",
            items: {
              bsonType: "object",
              required: ["id", "name", "price", "isAvailable"],
              properties: {
                id: { bsonType: "string", minLength: 1 },
                name: { bsonType: "string", minLength: 1 },
                description: { bsonType: "string" },
                price: {
                  bsonType: "object",
                  required: ["amount", "currency"],
                  properties: {
                    amount: { bsonType: "string", pattern: "^\\d+(\\.\\d+)?$" },
                    currency: { bsonType: "string", pattern: "^[A-Z]{3}$" },
                  },
                },
                isAvailable: { bsonType: "bool" },
              },
            },
          },
        },
      },
    },
    indexes: [
      {
        key: { restaurantId: 1 },
        name: "menus_restaurant_unique",
        unique: true,
      },
    ],
  });
}
