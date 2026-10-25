import assert from "node:assert/strict";
import test from "node:test";

import { PermissionDeniedError } from "../pkg/application/errors.ts";
import type { Subject } from "../pkg/application/subject.ts";
import {
  type ActiveRestaurantOrder,
  ListActiveRestaurantOrdersHandler,
} from "../slices/restaurantorder/applications/list-active-restaurant-orders/list-active-restaurant-orders.ts";
import { RestaurantOrderState } from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";

// List Active Restaurant Orders has only the MongoDB reader; the query is
// tested over mocks of its ports, the reader in grpc-kitchen-integration.test.ts.
const RESTAURANT = "restaurant-1";
const cook: Subject = { type: "user", id: "cook" };
const stranger: Subject = { type: "user", id: "stranger" };

const ACTIVE: readonly ActiveRestaurantOrder[] = [
  {
    restaurantOrderId: "restaurant-order-1",
    orderId: "order-1",
    state: RestaurantOrderState.Preparing,
    items: [{ menuItemId: "pho", quantity: 2, options: [] }],
    receivedAt: new Date("2026-10-25T12:00:00Z"),
    expectedReadyAt: new Date("2026-10-25T12:20:00Z"),
    version: 3,
  },
];

const createQuery = () => {
  const checks: Array<[string, string]> = [];
  const reads: string[] = [];
  const handler = new ListActiveRestaurantOrdersHandler({
    accessControl: {
      canOperateKitchen: async (subject, restaurantId) => {
        checks.push([subject.id, restaurantId]);
        return subject.id === cook.id;
      },
    },
    reader: {
      listActiveOrders: async (restaurantId) => {
        reads.push(restaurantId);
        return ACTIVE;
      },
    },
  });
  return { handler, checks, reads };
};

test("List Active Restaurant Orders returns the reader's orders of the restaurant to kitchen staff", async () => {
  const { handler, checks, reads } = createQuery();

  const result = await handler.execute({
    subject: cook,
    restaurantId: RESTAURANT,
  });

  assert.deepEqual(result, { restaurantId: RESTAURANT, orders: ACTIVE });
  assert.deepEqual(checks, [[cook.id, RESTAURANT]]);
  assert.deepEqual(reads, [RESTAURANT]);
});

test("List Active Restaurant Orders without restaurant#operate_kitchen is denied before reading", async () => {
  const { handler, reads } = createQuery();

  await assert.rejects(
    handler.execute({ subject: stranger, restaurantId: RESTAURANT }),
    PermissionDeniedError,
  );
  assert.deepEqual(reads, []);
});
