import assert from "node:assert/strict";
import { createServer } from "node:http2";
import test from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import {
  connectNodeAdapter,
  createGrpcTransport,
} from "@connectrpc/connect-node";

import {
  CancelRestaurantOrderRequestSchema,
  GetRestaurantRequestSchema,
  ReceiveOrderRequestRequestSchema,
  RestaurantsService,
} from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { createRestaurantsService } from "../infrastructure/grpc/service.ts";
import type { SubjectResolver } from "../infrastructure/grpc/subject.ts";
import { GrpcWorker } from "../infrastructure/grpc/worker.ts";
import { AuthorizationUnavailableError } from "../pkg/application/errors.ts";
import type { Subject } from "../pkg/application/subject.ts";
import { GetRestaurantHandler } from "../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import { createKitchenWorld, ordersService } from "./support/kitchen.ts";
import { PICKUP_ADDRESS } from "./support/restaurants.ts";

const context = undefined as never;

const resolverOf = (subject: Subject): SubjectResolver => ({
  resolve: async () => subject,
});

/** Service over the in-memory kitchen world with an `orders-worker` caller. */
const createWorld = (options: { subjects?: SubjectResolver | null } = {}) => {
  const world = createKitchenWorld();
  const service = createRestaurantsService({
    getRestaurant: new GetRestaurantHandler({ reader: world.catalog }),
    receiveOrderRequest: world.receive,
    cancelRestaurantOrder: world.cancel,
    registerRestaurant: world.register,
    assignStaff: world.assignStaff,
    revokeStaff: world.revokeStaff,
    subjects:
      options.subjects === null
        ? undefined
        : (options.subjects ?? resolverOf(ordersService)),
  });
  return { world, service };
};

const receiveRequest = (
  restaurantId: string,
  menuItemId: string,
  orderId = "order-42",
) =>
  create(ReceiveOrderRequestRequestSchema, {
    restaurantId,
    orderId,
    items: [{ menuItemId, quantity: 2 }],
  });

const rejectsWith = (code: Code) => (error: unknown) =>
  error instanceof ConnectError && error.code === code;

test("GetRestaurant maps the application view with the pickup address", async () => {
  const { world, service } = createWorld();
  const { restaurantId } = await world.givenKitchen("Pho Bar");

  const response = await service.getRestaurant(
    create(GetRestaurantRequestSchema, { restaurantId }),
    context,
  );

  assert.equal(response.restaurant?.restaurantId, restaurantId);
  assert.equal(response.restaurant?.name, "Pho Bar");
  assert.equal(response.restaurant?.isOpen, true);
  assert.equal(response.restaurant?.address, PICKUP_ADDRESS);
});

test("GetRestaurant maps missing restaurants to NOT_FOUND", async () => {
  const { service } = createWorld();

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
  const { world, service } = createWorld();
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
  assert.equal(world.orderStore.all().length, 1);
});

test("ReceiveOrderRequest maps a conflicting repeat to ALREADY_EXISTS", async () => {
  const { world, service } = createWorld();
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
        }),
        context,
      ),
    rejectsWith(Code.AlreadyExists),
  );
});

test("ReceiveOrderRequest maps invalid input to INVALID_ARGUMENT", async () => {
  const { world, service } = createWorld();
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
  assert.deepEqual(world.orderStore.all(), []);
});

test("ReceiveOrderRequest maps an unknown restaurant to NOT_FOUND", async () => {
  const { world, service } = createWorld();
  world.kitchenAccessControl.grantFulfillment(ordersService, "missing");

  await assert.rejects(
    async () =>
      service.receiveOrderRequest(receiveRequest("missing", "pho"), context),
    rejectsWith(Code.NotFound),
  );
});

test("commands map authorization outcomes to PERMISSION_DENIED and UNAVAILABLE", async () => {
  const { world, service } = createWorld({
    subjects: resolverOf({ type: "service", id: "stranger-worker" }),
  });
  const { restaurantId, phoId } = await world.givenKitchen();

  await assert.rejects(
    async () =>
      service.receiveOrderRequest(receiveRequest(restaurantId, phoId), context),
    rejectsWith(Code.PermissionDenied),
  );

  const unavailable = createRestaurantsService({
    getRestaurant: new GetRestaurantHandler({ reader: world.catalog }),
    receiveOrderRequest: {
      execute: async () => {
        throw new AuthorizationUnavailableError();
      },
    },
    cancelRestaurantOrder: world.cancel,
    registerRestaurant: world.register,
    assignStaff: world.assignStaff,
    revokeStaff: world.revokeStaff,
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
  const { world, service } = createWorld({ subjects: null });
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
  assert.deepEqual(world.orderStore.all(), []);
});

test("FR-428 CancelRestaurantOrder cancels once and answers a repeat as cancelled", async () => {
  const { world, service } = createWorld();
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
    world.outbox
      .eventTypes()
      .filter((type) => type === "RestaurantOrderCancelled").length,
    1,
  );
});

test("AC-454 CancelRestaurantOrder of a Ready order answers already_ready", async () => {
  const { world, service } = createWorld();
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
  const { world, service } = createWorld();
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

test("HTTP/2 worker serves the generated gRPC contract", async () => {
  const { world, service } = createWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const server = createServer(
    connectNodeAdapter({
      routes: (router) => router.service(RestaurantsService, service),
    }),
  );
  const worker = new GrpcWorker(server, 0);
  await worker.start();
  const running = worker.run();

  const client = createClient(
    RestaurantsService,
    createGrpcTransport({ baseUrl: `http://127.0.0.1:${worker.port}` }),
  );
  const restaurant = await client.getRestaurant({ restaurantId });
  assert.equal(restaurant.restaurant?.address, PICKUP_ADDRESS);
  const received = await client.receiveOrderRequest({
    restaurantId,
    orderId: "order-42",
    items: [{ menuItemId: phoId, quantity: 1 }],
  });
  assert.equal(received.created, true);
  const cancelled = await client.cancelRestaurantOrder({
    restaurantId,
    orderId: "order-42",
  });
  assert.equal(cancelled.cancelled, true);

  await worker.shutdown(AbortSignal.timeout(1_000));
  await running;
});
