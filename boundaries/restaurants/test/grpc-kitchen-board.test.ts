import assert from "node:assert/strict";
import test from "node:test";

import { timestampDate } from "@bufbuild/protobuf/wkt";
import { Code } from "@connectrpc/connect";

import { RestaurantOrderState } from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import type { SubjectResolver } from "../infrastructure/grpc/subject.ts";
import {
  type IncomingOrder,
  ListIncomingOrdersHandler,
} from "../slices/restaurantorder/applications/list-incoming-orders/list-incoming-orders.ts";
import {
  context,
  createWorld,
  rejectsWith,
  resolverOf,
} from "./support/grpc.ts";
import { cook, minutes } from "./support/kitchen.ts";

test("AC-737 GetKitchenQueue lists the received and accepted order for kitchen staff", async () => {
  const { world, service } = createWorld({ subjects: resolverOf(cook) });
  const { restaurantId, phoId } = await world.givenKitchen();
  const other = await world.givenKitchen("Noodle Bar");
  const waiting = await world.givenOrderIn("RECEIVED", restaurantId, phoId);
  const first = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  world.clock.advance(minutes(1));
  const second = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  await world.givenOrderIn("PREPARING", restaurantId, phoId);
  await world.givenOrderIn("QUEUED", other.restaurantId, other.phoId);

  const response = await service.getKitchenQueue({ restaurantId }, context);

  assert.deepEqual(
    response.entries.map((entry) => [
      entry.restaurantOrder?.restaurantOrderId,
      entry.restaurantOrder?.orderId,
    ]),
    [
      [first.restaurantOrderId, first.orderId],
      [second.restaurantOrderId, second.orderId],
    ],
  );
  assert.ok(
    response.entries.every(
      (entry) =>
        entry.restaurantOrder?.restaurantOrderId !== waiting.restaurantOrderId,
    ),
  );
  const [entry] = response.entries;
  const stored = await world.restaurantOrders.get(first.restaurantOrderId);
  assert.equal(entry?.restaurantOrder?.restaurantId, restaurantId);
  assert.equal(entry?.restaurantOrder?.state, RestaurantOrderState.QUEUED);
  assert.equal(entry?.restaurantOrder?.version, BigInt(stored?.version ?? 0));
  assert.ok(entry?.acceptedAt !== undefined && entry.queuedAt !== undefined);
  const expectedReadyAt = entry?.restaurantOrder?.expectedReadyAt;
  assert.ok(expectedReadyAt !== undefined);
  assert.ok(
    timestampDate(expectedReadyAt).getTime() >
      timestampDate(entry.acceptedAt).getTime(),
  );
});

test("AC-737 GetKitchenQueue of a restaurant with nothing queued is empty", async () => {
  const { world, service } = createWorld({ subjects: resolverOf(cook) });
  const { restaurantId, phoId } = await world.givenKitchen();
  await world.givenOrderIn("RECEIVED", restaurantId, phoId);

  const response = await service.getKitchenQueue({ restaurantId }, context);

  assert.deepEqual(response.entries, []);
});

test("AC-738 GetKitchenQueue without restaurant#operate_kitchen is PERMISSION_DENIED", async () => {
  const { world, service } = createWorld({
    subjects: resolverOf({ type: "user", id: "stranger" }),
  });
  const { restaurantId, phoId } = await world.givenKitchen();
  await world.givenOrderIn("QUEUED", restaurantId, phoId);

  await assert.rejects(
    async () => service.getKitchenQueue({ restaurantId }, context),
    rejectsWith(Code.PermissionDenied),
  );
});

test("AC-738 GetKitchenQueue without a verified identity is UNAUTHENTICATED", async () => {
  const { world, service } = createWorld({ subjects: null });
  const { restaurantId } = await world.givenKitchen();

  await assert.rejects(
    async () => service.getKitchenQueue({ restaurantId }, context),
    rejectsWith(Code.Unauthenticated),
  );
});

test("GetKitchenQueue without restaurant_id is INVALID_ARGUMENT", async () => {
  const { service } = createWorld({ subjects: resolverOf(cook) });

  await assert.rejects(
    async () => service.getKitchenQueue({ restaurantId: "" }, context),
    rejectsWith(Code.InvalidArgument),
  );
});

// ListIncomingOrders is read only from MongoDB: the transport is tested over a
// fake reader port, the adapter in grpc-kitchen-integration.test.ts.
const INCOMING_RESTAURANT = "restaurant-incoming";
const RECEIVED_AT = new Date("2026-10-25T12:00:00Z");
const INCOMING: readonly IncomingOrder[] = [
  {
    restaurantOrderId: "restaurant-order-1",
    orderId: "order-1",
    items: [{ menuItemId: "pho", quantity: 2, options: ["spicy"] }],
    receivedAt: RECEIVED_AT,
    version: 1,
  },
  {
    restaurantOrderId: "restaurant-order-2",
    orderId: "order-2",
    items: [{ menuItemId: "tea", quantity: 1, options: [] }],
    receivedAt: new Date(RECEIVED_AT.getTime() + minutes(1)),
    version: 1,
  },
];

/** Service whose ListIncomingOrders reads a fake port; `cook` is staff. */
const createIncomingService = (subjects: SubjectResolver | null) => {
  const reads: string[] = [];
  const { service } = createWorld({
    subjects,
    listIncomingOrders: new ListIncomingOrdersHandler({
      accessControl: {
        canOperateKitchen: async (subject, restaurantId) =>
          subject.id === cook.id && restaurantId === INCOMING_RESTAURANT,
      },
      reader: {
        listIncoming: async (restaurantId) => {
          reads.push(restaurantId);
          return restaurantId === INCOMING_RESTAURANT ? INCOMING : [];
        },
      },
    }),
  });
  return { service, reads };
};

test("AC-742 ListIncomingOrders answers the received orders with ids, items, version and received_at", async () => {
  const { service, reads } = createIncomingService(resolverOf(cook));

  const response = await service.listIncomingOrders(
    { restaurantId: INCOMING_RESTAURANT },
    context,
  );

  assert.deepEqual(reads, [INCOMING_RESTAURANT]);
  assert.deepEqual(
    response.orders.map((order) => [
      order.restaurantOrder?.restaurantOrderId,
      order.restaurantOrder?.orderId,
    ]),
    [
      ["restaurant-order-1", "order-1"],
      ["restaurant-order-2", "order-2"],
    ],
  );
  const [incoming] = response.orders;
  assert.equal(incoming?.restaurantOrder?.restaurantId, INCOMING_RESTAURANT);
  assert.equal(incoming?.restaurantOrder?.state, RestaurantOrderState.RECEIVED);
  assert.equal(incoming?.restaurantOrder?.version, 1n);
  assert.equal(incoming?.restaurantOrder?.expectedReadyAt, undefined);
  assert.deepEqual(
    incoming?.items.map((item) => [
      item.menuItemId,
      item.quantity,
      item.options,
    ]),
    [["pho", 2, ["spicy"]]],
  );
  assert.ok(incoming?.receivedAt !== undefined);
  assert.equal(
    timestampDate(incoming.receivedAt).getTime(),
    RECEIVED_AT.getTime(),
  );
});

test("AC-743 ListIncomingOrders without restaurant#operate_kitchen is PERMISSION_DENIED", async () => {
  const { service, reads } = createIncomingService(
    resolverOf({ type: "user", id: "stranger" }),
  );

  await assert.rejects(
    async () =>
      service.listIncomingOrders(
        { restaurantId: INCOMING_RESTAURANT },
        context,
      ),
    rejectsWith(Code.PermissionDenied),
  );
  assert.deepEqual(reads, []);
});

test("AC-743 ListIncomingOrders without a verified identity is UNAUTHENTICATED", async () => {
  const { service, reads } = createIncomingService(null);

  await assert.rejects(
    async () =>
      service.listIncomingOrders(
        { restaurantId: INCOMING_RESTAURANT },
        context,
      ),
    rejectsWith(Code.Unauthenticated),
  );
  assert.deepEqual(reads, []);
});

test("ListIncomingOrders without restaurant_id is INVALID_ARGUMENT", async () => {
  const { service, reads } = createIncomingService(resolverOf(cook));

  await assert.rejects(
    async () => service.listIncomingOrders({ restaurantId: "" }, context),
    rejectsWith(Code.InvalidArgument),
  );
  assert.deepEqual(reads, []);
});
