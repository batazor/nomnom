import assert from "node:assert/strict";
import test from "node:test";

import { PermissionDeniedError } from "../pkg/application/errors.ts";
import type { Subject } from "../pkg/application/subject.ts";
import {
  type IncomingOrder,
  ListIncomingOrdersHandler,
} from "../slices/restaurantorder/applications/list-incoming-orders/list-incoming-orders.ts";

// List Incoming Orders has only the MongoDB reader; the query is tested over
// mocks of its ports, the reader in grpc-kitchen-integration.test.ts.
const RESTAURANT = "restaurant-1";
const cook: Subject = { type: "user", id: "cook" };
const stranger: Subject = { type: "user", id: "stranger" };

const INCOMING: readonly IncomingOrder[] = [
  {
    restaurantOrderId: "restaurant-order-1",
    orderId: "order-1",
    items: [{ menuItemId: "pho", quantity: 2, options: [] }],
    receivedAt: new Date("2026-10-25T12:00:00Z"),
    version: 1,
  },
];

const createQuery = () => {
  const checks: Array<[string, string]> = [];
  const reads: string[] = [];
  const handler = new ListIncomingOrdersHandler({
    accessControl: {
      canOperateKitchen: async (subject, restaurantId) => {
        checks.push([subject.id, restaurantId]);
        return subject.id === cook.id;
      },
    },
    reader: {
      listIncoming: async (restaurantId) => {
        reads.push(restaurantId);
        return INCOMING;
      },
    },
  });
  return { handler, checks, reads };
};

test("AC-742 List Incoming Orders returns the reader's orders of the restaurant to kitchen staff", async () => {
  const { handler, checks, reads } = createQuery();

  const result = await handler.execute({
    subject: cook,
    restaurantId: RESTAURANT,
  });

  assert.deepEqual(result, { restaurantId: RESTAURANT, orders: INCOMING });
  assert.deepEqual(checks, [[cook.id, RESTAURANT]]);
  assert.deepEqual(reads, [RESTAURANT]);
});

test("AC-743 List Incoming Orders without restaurant#operate_kitchen is denied before reading", async () => {
  const { handler, reads } = createQuery();

  await assert.rejects(
    handler.execute({ subject: stranger, restaurantId: RESTAURANT }),
    PermissionDeniedError,
  );
  assert.deepEqual(reads, []);
});
