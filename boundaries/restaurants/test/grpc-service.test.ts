import assert from "node:assert/strict";
import test from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code } from "@connectrpc/connect";

import {
  BatchGetMenusRequestSchema,
  BatchGetRestaurantsRequestSchema,
  CancelRestaurantOrderRequestSchema,
  GetMenuRequestSchema,
  GetRestaurantRequestSchema,
  ListOwnedRestaurantsRequestSchema,
  ListRestaurantsRequestSchema,
  OrderPriority,
  ReceiveOrderRequestRequestSchema,
} from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { AuthorizationUnavailableError } from "../pkg/application/errors.ts";
import { BatchGetRestaurantsHandler } from "../slices/restaurant/applications/batch-get-restaurants/batch-get-restaurants.ts";
import { GetRestaurantHandler } from "../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import { ListOwnedRestaurantsHandler } from "../slices/restaurant/applications/list-owned-restaurants/list-owned-restaurants.ts";
import { ListRestaurantsHandler } from "../slices/restaurant/applications/list-restaurants/list-restaurants.ts";
import {
  context,
  createService,
  createWorld,
  rejectsWith,
  resolverOf,
} from "./support/grpc.ts";
import { ordersService } from "./support/kitchen.ts";

const receiveRequest = (
  restaurantId: string,
  menuItemId: string,
  orderId = "order-42",
) =>
  create(ReceiveOrderRequestRequestSchema, {
    restaurantId,
    orderId,
    items: [{ menuItemId, quantity: 2 }],
    priority: OrderPriority.STANDARD,
  });

test("GetRestaurant maps the application view", async () => {
  const { world, service } = await createWorld();
  const { restaurantId } = await world.givenKitchen("Pho Bar");

  const response = await service.getRestaurant(
    create(GetRestaurantRequestSchema, { restaurantId }),
    context,
  );

  assert.equal(response.restaurant?.restaurantId, restaurantId);
  assert.equal(response.restaurant?.name, "Pho Bar");
  assert.equal(response.restaurant?.isOpen, true);
  assert.equal(response.restaurant?.marketId, "berlin");
});

test("FR-493 GetMenu returns the public menu with prices and availability", async () => {
  const { world, service } = await createWorld();
  const { restaurantId, phoId } = await world.givenKitchen("Pho Bar");

  const response = await service.getMenu(
    create(GetMenuRequestSchema, { restaurantId }),
    context,
  );

  const pho = response.items.find((item) => item.menuItemId === phoId);
  assert.equal(pho?.name, "Pho");
  assert.equal(pho?.available, true);
  assert.deepEqual(
    { amount: pho?.price?.amount, currency: pho?.price?.currency },
    { amount: "9.50", currency: "EUR" },
  );
});

test("GetMenu maps a restaurant without a menu to NOT_FOUND", async () => {
  const { service } = await createWorld();

  await assert.rejects(
    async () =>
      service.getMenu(
        create(GetMenuRequestSchema, { restaurantId: "missing" }),
        context,
      ),
    rejectsWith(Code.NotFound),
  );
});

test("BatchGetRestaurants returns the found restaurants of a page in one call", async () => {
  const { world, service } = await createWorld();
  const pho = await world.givenRestaurant("Pho Bar");
  const ramen = await world.givenRestaurant("Ramen Bar");

  const response = await service.batchGetRestaurants(
    create(BatchGetRestaurantsRequestSchema, {
      restaurantIds: [pho, ramen, pho, "missing"],
    }),
    context,
  );

  assert.deepEqual(
    response.restaurants.map((restaurant) => restaurant.name).sort(),
    ["Pho Bar", "Ramen Bar"],
  );
});

test("BatchGetRestaurants without ids is INVALID_ARGUMENT", async () => {
  const { service } = await createWorld();

  await assert.rejects(
    async () =>
      service.batchGetRestaurants(
        create(BatchGetRestaurantsRequestSchema, { restaurantIds: [] }),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
  );
});

test("BatchGetMenus with more than 100 ids is INVALID_ARGUMENT", async () => {
  const { service } = await createWorld();

  await assert.rejects(
    async () =>
      service.batchGetMenus(
        create(BatchGetMenusRequestSchema, {
          restaurantIds: Array.from({ length: 101 }, (_, i) => `r-${i}`),
        }),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
  );
});

test("BatchGetMenus returns the menus by restaurant; a restaurant without a menu is absent", async () => {
  const { world, service } = await createWorld();
  const { restaurantId, phoId } = await world.givenKitchen("Pho Bar");
  const withoutMenu = await world.givenRestaurant("Ramen Bar");

  const response = await service.batchGetMenus(
    create(BatchGetMenusRequestSchema, {
      restaurantIds: [restaurantId, withoutMenu],
    }),
    context,
  );

  assert.equal(response.menus.length, 1);
  assert.equal(response.menus[0]?.restaurantId, restaurantId);
  assert.ok(response.menus[0]?.items.some((item) => item.menuItemId === phoId));
});

test("AC-565 ListRestaurants pages open restaurants with a next page token", async () => {
  const { world, service } = await createWorld();
  await world.givenRestaurant("Pho Bar");
  await world.givenRestaurant("Ramen Bar");
  await world.givenRestaurant("Closed Bar", { open: false });

  const first = await service.listRestaurants(
    create(ListRestaurantsRequestSchema, { marketId: "berlin", pageSize: 1 }),
    context,
  );

  assert.equal(first.restaurants.length, 1);
  assert.notEqual(first.nextPageToken, "");

  const second = await service.listRestaurants(
    create(ListRestaurantsRequestSchema, {
      marketId: "berlin",
      pageSize: 1,
      pageToken: first.nextPageToken,
    }),
    context,
  );

  assert.equal(second.restaurants.length, 1);
  assert.equal(second.nextPageToken, "");
  assert.deepEqual(
    [...first.restaurants, ...second.restaurants]
      .map((r) => [r.name, r.marketId])
      .sort(),
    [
      ["Pho Bar", "berlin"],
      ["Ramen Bar", "berlin"],
    ],
  );
});

test("ADR 41 ListRestaurants returns only the requested Market and requires it", async () => {
  const { world, service } = await createWorld();
  await world.givenRestaurant("Pho Bar");
  await world.givenRestaurant("Hamburg Bar", {
    open: true,
    marketId: "hamburg",
  });

  const hamburg = await service.listRestaurants(
    create(ListRestaurantsRequestSchema, { marketId: "hamburg" }),
    context,
  );

  assert.deepEqual(
    hamburg.restaurants.map((r) => [r.name, r.marketId]),
    [["Hamburg Bar", "hamburg"]],
  );
  for (const marketId of ["", "Hamburg", "h"]) {
    await assert.rejects(
      async () =>
        service.listRestaurants(
          create(ListRestaurantsRequestSchema, { marketId }),
          context,
        ),
      rejectsWith(Code.InvalidArgument),
    );
  }
});

test("ListOwnedRestaurants lists restaurants of the calling owner", async () => {
  const { world, service } = await createWorld({
    subjects: resolverOf({ type: "user", id: "owner" }),
  });
  await world.givenRestaurant("Pho Bar");
  await world.givenRestaurant("Closed Bar", { open: false });
  await world.givenRestaurant("Hamburg Bar", {
    open: true,
    marketId: "hamburg",
  });

  const response = await service.listOwnedRestaurants(
    create(ListOwnedRestaurantsRequestSchema, { marketId: "berlin" }),
    context,
  );

  assert.deepEqual(response.restaurants.map((r) => r.name).sort(), [
    "Closed Bar",
    "Pho Bar",
  ]);
  assert.equal(response.nextPageToken, "");
  for (const marketId of ["", "Berlin"]) {
    await assert.rejects(
      async () =>
        service.listOwnedRestaurants(
          create(ListOwnedRestaurantsRequestSchema, { marketId }),
          context,
        ),
      rejectsWith(Code.InvalidArgument),
    );
  }
});

test("ListOwnedRestaurants rejects a service subject with PERMISSION_DENIED", async () => {
  const { service } = await createWorld({
    subjects: resolverOf({ type: "service", id: "orders" }),
  });

  await assert.rejects(
    async () =>
      service.listOwnedRestaurants(
        create(ListOwnedRestaurantsRequestSchema, { marketId: "berlin" }),
        context,
      ),
    rejectsWith(Code.PermissionDenied),
  );
});

test("ListRestaurants includes closed restaurants on request with the default page size", async () => {
  const { world, service } = await createWorld();
  await world.givenRestaurant("Pho Bar");
  await world.givenRestaurant("Closed Bar", { open: false });

  const response = await service.listRestaurants(
    create(ListRestaurantsRequestSchema, {
      marketId: "berlin",
      includeClosed: true,
    }),
    context,
  );

  assert.deepEqual(response.restaurants.map((r) => [r.name, r.isOpen]).sort(), [
    ["Closed Bar", false],
    ["Pho Bar", true],
  ]);
  assert.equal(response.nextPageToken, "");
});

test("ListRestaurants maps a page size out of range to INVALID_ARGUMENT", async () => {
  const { service } = await createWorld();

  await assert.rejects(
    async () =>
      service.listRestaurants(
        create(ListRestaurantsRequestSchema, {
          marketId: "berlin",
          pageSize: 101,
        }),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
  );
});

test("GetRestaurant maps missing restaurants to NOT_FOUND", async () => {
  const { service } = await createWorld();

  await assert.rejects(
    async () =>
      service.getRestaurant(
        create(GetRestaurantRequestSchema, { restaurantId: "missing" }),
        context,
      ),
    rejectsWith(Code.NotFound),
  );
});

test("FR-428 ReceiveOrderRequest is idempotent by order_id", async () => {
  const { world, service } = await createWorld();
  const { restaurantId, phoId } = await world.givenKitchen();

  const first = await service.receiveOrderRequest(
    receiveRequest(restaurantId, phoId),
    context,
  );
  const repeated = await service.receiveOrderRequest(
    receiveRequest(restaurantId, phoId),
    context,
  );

  assert.equal(first.created, true);
  assert.notEqual(first.restaurantOrderId, "");
  assert.equal(repeated.created, false);
  assert.equal(repeated.restaurantOrderId, first.restaurantOrderId);
  assert.equal((await world.orderStore.find({})).length, 1);
});

test("ReceiveOrderRequest maps a conflicting repeat to ALREADY_EXISTS", async () => {
  const { world, service } = await createWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  await service.receiveOrderRequest(
    receiveRequest(restaurantId, phoId),
    context,
  );

  await assert.rejects(
    async () =>
      service.receiveOrderRequest(
        create(ReceiveOrderRequestRequestSchema, {
          restaurantId,
          orderId: "order-42",
          items: [{ menuItemId: phoId, quantity: 5 }],
          priority: OrderPriority.STANDARD,
        }),
        context,
      ),
    rejectsWith(Code.AlreadyExists),
  );
});

test("AC-859 ReceiveOrderRequest without priority is INVALID_ARGUMENT, PREMIUM is kept", async () => {
  const { world, service } = await createWorld();
  const { restaurantId, phoId } = await world.givenKitchen();

  await assert.rejects(
    async () =>
      service.receiveOrderRequest(
        create(ReceiveOrderRequestRequestSchema, {
          restaurantId,
          orderId: "order-unspecified",
          items: [{ menuItemId: phoId, quantity: 1 }],
        }),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
  );
  assert.equal(
    await world.restaurantOrders.findByOrderId("order-unspecified"),
    undefined,
  );

  await service.receiveOrderRequest(
    create(ReceiveOrderRequestRequestSchema, {
      restaurantId,
      orderId: "order-premium",
      items: [{ menuItemId: phoId, quantity: 1 }],
      priority: OrderPriority.PREMIUM,
    }),
    context,
  );
  assert.equal(
    (await world.restaurantOrders.findByOrderId("order-premium"))?.priority,
    "PREMIUM",
  );
});

test("ReceiveOrderRequest maps invalid input to INVALID_ARGUMENT", async () => {
  const { world, service } = await createWorld();
  const { restaurantId, phoId } = await world.givenKitchen();

  await assert.rejects(
    async () =>
      service.receiveOrderRequest(
        create(ReceiveOrderRequestRequestSchema, {
          restaurantId,
          orderId: "o",
        }),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
  );
  await assert.rejects(
    async () =>
      service.receiveOrderRequest(
        create(ReceiveOrderRequestRequestSchema, {
          restaurantId,
          orderId: "o",
          items: [{ menuItemId: phoId, quantity: 0 }],
          priority: OrderPriority.STANDARD,
        }),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
  );
  await assert.rejects(
    async () =>
      service.receiveOrderRequest(
        receiveRequest(restaurantId, phoId, ""),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
  );
  assert.deepEqual(await world.orderStore.find({}), []);
});

test("ReceiveOrderRequest maps an unknown restaurant to NOT_FOUND", async () => {
  const { world, service } = await createWorld();
  world.kitchenAccessControl.grantFulfillment(ordersService, "missing");

  await assert.rejects(
    async () =>
      service.receiveOrderRequest(receiveRequest("missing", "pho"), context),
    rejectsWith(Code.NotFound),
  );
});

test("commands map authorization outcomes to PERMISSION_DENIED and UNAVAILABLE", async () => {
  const { world, service } = await createWorld({
    subjects: resolverOf({ type: "service", id: "stranger-worker" }),
  });
  const { restaurantId, phoId } = await world.givenKitchen();

  await assert.rejects(
    async () =>
      service.receiveOrderRequest(receiveRequest(restaurantId, phoId), context),
    rejectsWith(Code.PermissionDenied),
  );

  const unavailable = createService({
    getRestaurant: new GetRestaurantHandler({ reader: world.catalog }),
    getMenu: world.getMenu,
    batchGetRestaurants: new BatchGetRestaurantsHandler({
      reader: world.catalog,
    }),
    batchGetMenus: world.batchGetMenus,
    listRestaurants: new ListRestaurantsHandler({ reader: world.catalog }),
    listOwnedRestaurants: new ListOwnedRestaurantsHandler({
      reader: world.catalog,
    }),
    receiveOrderRequest: {
      execute: async () => {
        throw new AuthorizationUnavailableError();
      },
    },
    cancelRestaurantOrder: world.cancel,
    acceptRestaurantOrder: world.accept,
    rejectRestaurantOrder: world.reject,
    startPreparation: world.startPreparation,
    markRestaurantOrderAssembled: world.markAssembled,
    markRestaurantOrderReady: world.markReady,
    reportPreparationDelay: world.reportDelay,
    reportPreparationFailure: world.reportFailure,
    getKitchenQueue: world.getKitchenQueue,
    listIncomingOrders: { execute: async () => assert.fail("unexpected call") },
    listActiveRestaurantOrders: {
      execute: async () => assert.fail("unexpected call"),
    },
    registerRestaurant: world.register,
    updateRestaurant: world.update,
    assignStaff: world.assignStaff,
    revokeStaff: world.revokeStaff,
    openRestaurant: world.open,
    closeRestaurant: world.close,
    createMenu: world.createMenu,
    addMenuItem: world.addMenuItem,
    updateMenuItem: world.updateMenuItem,
    setMenuItemAvailability: world.setAvailability,
    getSimulator: { execute: () => Promise.reject(new Error("unused")) },
    setSimulatorEnabled: { execute: () => Promise.reject(new Error("unused")) },
    hireCook: { execute: () => Promise.reject(new Error("unused")) },
    putCookOnLine: { execute: () => Promise.reject(new Error("unused")) },
    takeCookOffLine: { execute: () => Promise.reject(new Error("unused")) },
    retireCook: { execute: () => Promise.reject(new Error("unused")) },
    getCookPool: { execute: () => Promise.reject(new Error("unused")) },
    subjects: resolverOf(ordersService),
  });
  await assert.rejects(
    async () =>
      unavailable.receiveOrderRequest(
        receiveRequest(restaurantId, phoId),
        context,
      ),
    rejectsWith(Code.Unavailable),
  );
});

test("commands are UNAUTHENTICATED with the default rejecting resolver", async () => {
  const { world, service } = await createWorld({ subjects: null });
  const { restaurantId, phoId } = await world.givenKitchen();

  await assert.rejects(
    async () =>
      service.receiveOrderRequest(receiveRequest(restaurantId, phoId), context),
    rejectsWith(Code.Unauthenticated),
  );
  await assert.rejects(
    async () =>
      service.cancelRestaurantOrder(
        create(CancelRestaurantOrderRequestSchema, {
          restaurantId,
          orderId: "order-42",
        }),
        context,
      ),
    rejectsWith(Code.Unauthenticated),
  );
  assert.deepEqual(await world.orderStore.find({}), []);
});

test("FR-428 CancelRestaurantOrder cancels once and answers a repeat as cancelled", async () => {
  const { world, service } = await createWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const received = await service.receiveOrderRequest(
    receiveRequest(restaurantId, phoId),
    context,
  );
  const request = create(CancelRestaurantOrderRequestSchema, {
    restaurantId,
    orderId: "order-42",
    reason: "payment_failed",
  });

  const first = await service.cancelRestaurantOrder(request, context);
  const repeated = await service.cancelRestaurantOrder(request, context);

  for (const response of [first, repeated]) {
    assert.equal(response.restaurantOrderId, received.restaurantOrderId);
    assert.equal(response.cancelled, true);
    assert.equal(response.rejectionReason, "");
  }
  assert.equal(
    (await world.outbox.eventTypes()).filter(
      (type) => type === "RestaurantOrderCancelled",
    ).length,
    1,
  );
});

test("AC-454 CancelRestaurantOrder of a Ready order answers already_ready", async () => {
  const { world, service } = await createWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const ready = await world.givenOrderIn("READY", restaurantId, phoId);

  const response = await service.cancelRestaurantOrder(
    create(CancelRestaurantOrderRequestSchema, {
      restaurantId,
      orderId: ready.orderId,
    }),
    context,
  );

  assert.equal(response.restaurantOrderId, ready.restaurantOrderId);
  assert.equal(response.cancelled, false);
  assert.equal(response.rejectionReason, "already_ready");
});

test("CancelRestaurantOrder of an unknown order is NOT_FOUND", async () => {
  const { world, service } = await createWorld();
  const { restaurantId } = await world.givenKitchen();

  await assert.rejects(
    async () =>
      service.cancelRestaurantOrder(
        create(CancelRestaurantOrderRequestSchema, {
          restaurantId,
          orderId: "missing",
        }),
        context,
      ),
    rejectsWith(Code.NotFound),
  );
});
