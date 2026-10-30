import assert from "node:assert/strict";
import test from "node:test";

import { PermissionDeniedError } from "../pkg/authz/errors.ts";
import { AlreadyExistsError, NotFoundError } from "../pkg/usecase/errors.ts";
import {
  InvalidRestaurantOrderError,
  RestaurantOrder,
} from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import { cook, createKitchenWorld, ordersService } from "./support/kitchen.ts";

test("AC-242 receive order request creates a RECEIVED restaurant order", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const receivedAt = world.clock.now();

  const result = await world.receive.execute({
    subject: ordersService,
    restaurantId,
    orderId: "order-42",
    items: [{ menuItemId: phoId, quantity: 2, options: ["spicy"] }],
    priority: "STANDARD",
  });

  assert.equal(result.created, true);
  assert.deepEqual(result.restaurantOrder, {
    restaurantOrderId: "restaurant-order-1",
    orderId: "order-42",
    restaurantId,
    state: "RECEIVED",
    items: [{ menuItemId: phoId, quantity: 2, options: ["spicy"] }],
    timeline: { receivedAt },
    priority: "STANDARD",
    schedule: undefined,
    cookId: undefined,
    rejectionReason: undefined,
    failureReason: undefined,
    cancelledAtStage: undefined,
    version: 1,
  });
});

test("AC-858 a premium order is received with its priority", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();

  const result = await world.receive.execute({
    subject: ordersService,
    restaurantId,
    orderId: "order-premium",
    items: [{ menuItemId: phoId, quantity: 1 }],
    priority: "PREMIUM",
  });

  assert.equal(result.restaurantOrder.priority, "PREMIUM");
  assert.equal(
    (await world.restaurantOrders.findByOrderId("order-premium"))?.priority,
    "PREMIUM",
  );
});

test("AC-859 an order without a known priority is not received", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();

  for (const priority of ["", "URGENT"]) {
    await assert.rejects(
      world.receive.execute({
        subject: ordersService,
        restaurantId,
        orderId: `order-${priority}`,
        items: [{ menuItemId: phoId, quantity: 1 }],
        priority,
      }),
      InvalidRestaurantOrderError,
    );
  }
  assert.deepEqual(await world.orderStore.find({}), []);
});

test("ADR 41 a received order takes the Market of its restaurant", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();

  const named = await world.receive.execute({
    subject: ordersService,
    restaurantId,
    orderId: "order-named",
    items: [{ menuItemId: phoId, quantity: 1 }],
    priority: "STANDARD",
  });
  await world.receive.execute({
    subject: ordersService,
    restaurantId,
    orderId: "order-unnamed",
    items: [{ menuItemId: phoId, quantity: 1 }],
    priority: "STANDARD",
  });

  assert.equal(named.created, true);
  for (const orderId of ["order-named", "order-unnamed"]) {
    const order = await world.restaurantOrders.findByOrderId(orderId);
    assert.equal(order?.marketId.value, "berlin");
    assert.equal(order?.toSnapshot().marketId, "berlin");
  }
});

test("AC-243 a repeated receive returns the existing order", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const request = {
    subject: ordersService,
    restaurantId,
    orderId: "order-42",
    items: [{ menuItemId: phoId, quantity: 2 }],
    priority: "STANDARD",
  };

  const first = await world.receive.execute(request);
  const repeated = await world.receive.execute(request);

  assert.equal(repeated.created, false);
  assert.deepEqual(repeated.restaurantOrder, first.restaurantOrder);
  assert.equal((await world.orderStore.find({})).length, 1);
});

test("AC-244 a repeat with another composition or priority is rejected as already exists", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  await world.receive.execute({
    subject: ordersService,
    restaurantId,
    orderId: "order-42",
    items: [{ menuItemId: phoId, quantity: 2 }],
    priority: "STANDARD",
  });

  await assert.rejects(
    world.receive.execute({
      subject: ordersService,
      restaurantId,
      orderId: "order-42",
      items: [{ menuItemId: phoId, quantity: 3 }],
      priority: "STANDARD",
    }),
    AlreadyExistsError,
  );
  // Another priority is another request too (FR-651).
  await assert.rejects(
    world.receive.execute({
      subject: ordersService,
      restaurantId,
      orderId: "order-42",
      items: [{ menuItemId: phoId, quantity: 2 }],
      priority: "PREMIUM",
    }),
    AlreadyExistsError,
  );
});

test("concurrent receive that loses the unique orderId race returns the winner", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const items = [{ menuItemId: phoId, quantity: 1 }];
  const original = world.restaurantOrders.findByOrderId.bind(
    world.restaurantOrders,
  );
  let firstLookup = true;
  world.restaurantOrders.findByOrderId = async (orderId) => {
    if (firstLookup) {
      firstLookup = false;
      const winner = RestaurantOrder.receive({
        id: "winner",
        orderId,
        restaurantId,
        marketId: "berlin",
        items,
        priority: "STANDARD",
        receivedAt: world.clock.now(),
      });
      await world.restaurantOrders.save(winner);
      return undefined;
    }
    return original(orderId);
  };

  const result = await world.receive.execute({
    subject: ordersService,
    restaurantId,
    orderId: "order-42",
    items,
    priority: "STANDARD",
  });

  assert.equal(result.created, false);
  assert.equal(result.restaurantOrder.restaurantOrderId, "winner");
  assert.equal((await world.orderStore.find({})).length, 1);
});

test("AC-245 invalid composition is rejected and nothing is stored", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();

  for (const items of [
    [],
    [{ menuItemId: phoId, quantity: 0 }],
    [
      { menuItemId: phoId, quantity: 1, options: ["a"] },
      { menuItemId: phoId, quantity: 1, options: ["a"] },
    ],
  ]) {
    await assert.rejects(
      world.receive.execute({
        subject: ordersService,
        restaurantId,
        orderId: "order-42",
        items,
        priority: "STANDARD",
      }),
      InvalidRestaurantOrderError,
    );
  }
  assert.deepEqual(await world.orderStore.find({}), []);
});

test("receive order request requires the fulfillment service identity and a known restaurant", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  world.kitchenAccessControl.grantFulfillment(ordersService, "missing");

  await assert.rejects(
    world.receive.execute({
      subject: cook,
      restaurantId,
      orderId: "order-42",
      items: [{ menuItemId: phoId, quantity: 1 }],
      priority: "STANDARD",
    }),
    PermissionDeniedError,
  );
  await assert.rejects(
    world.receive.execute({
      subject: ordersService,
      restaurantId: "missing",
      orderId: "order-42",
      items: [{ menuItemId: phoId, quantity: 1 }],
      priority: "STANDARD",
    }),
    NotFoundError,
  );
  assert.deepEqual(await world.orderStore.find({}), []);
});
