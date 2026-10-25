import assert from "node:assert/strict";
import { createServer } from "node:http2";
import test from "node:test";

import { create } from "@bufbuild/protobuf";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import {
  connectNodeAdapter,
  createGrpcTransport,
} from "@connectrpc/connect-node";

import {
  CancelRestaurantOrderRequestSchema,
  GetRestaurantRequestSchema,
  ReceiveOrderRequestRequestSchema,
  RestaurantOrderState,
  RestaurantsService,
} from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { createRestaurantsService } from "../infrastructure/grpc/service.ts";
import type { SubjectResolver } from "../infrastructure/grpc/subject.ts";
import { GrpcWorker } from "../infrastructure/grpc/worker.ts";
import { AuthorizationUnavailableError } from "../pkg/application/errors.ts";
import type { Subject } from "../pkg/application/subject.ts";
import { GetRestaurantHandler } from "../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import {
  cook,
  createKitchenWorld,
  minutes,
  ordersService,
  PREPARATION_MINUTES,
} from "./support/kitchen.ts";
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
    acceptRestaurantOrder: world.accept,
    rejectRestaurantOrder: world.reject,
    startPreparation: world.startPreparation,
    markRestaurantOrderAssembled: world.markAssembled,
    markRestaurantOrderReady: world.markReady,
    reportPreparationDelay: world.reportDelay,
    reportPreparationFailure: world.reportFailure,
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
    acceptRestaurantOrder: world.accept,
    rejectRestaurantOrder: world.reject,
    startPreparation: world.startPreparation,
    markRestaurantOrderAssembled: world.markAssembled,
    markRestaurantOrderReady: world.markReady,
    reportPreparationDelay: world.reportDelay,
    reportPreparationFailure: world.reportFailure,
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

type KitchenStage = Parameters<
  ReturnType<typeof createKitchenWorld>["givenOrderIn"]
>[0];
type KitchenService = ReturnType<typeof createWorld>["service"];
type Addressed = { restaurantId: string; restaurantOrderId: string };

/** Kitchen command RPCs: the stage they act on, the call and the outcome. */
const KITCHEN_RPCS: ReadonlyArray<{
  rpc: string;
  from: KitchenStage;
  invalidFrom: KitchenStage;
  call: (
    service: KitchenService,
    order: Addressed,
    now: Date,
  ) => Promise<{
    restaurantOrder?: {
      restaurantOrderId: string;
      state: RestaurantOrderState;
    };
  }>;
  state: RestaurantOrderState;
  published: string | undefined;
}> = [
  {
    rpc: "AcceptRestaurantOrder",
    from: "RECEIVED",
    invalidFrom: "PREPARING",
    call: (service, order) => service.acceptRestaurantOrder(order, context),
    state: RestaurantOrderState.QUEUED,
    published: "RestaurantOrderAccepted",
  },
  {
    rpc: "RejectRestaurantOrder",
    from: "RECEIVED",
    invalidFrom: "QUEUED",
    call: (service, order) =>
      service.rejectRestaurantOrder({ ...order, reason: "too_busy" }, context),
    state: RestaurantOrderState.REJECTED,
    published: "RestaurantOrderRejected",
  },
  {
    rpc: "StartPreparation",
    from: "QUEUED",
    invalidFrom: "RECEIVED",
    call: (service, order) => service.startPreparation(order, context),
    state: RestaurantOrderState.PREPARING,
    published: "PreparationStarted",
  },
  {
    rpc: "MarkRestaurantOrderAssembled",
    from: "PREPARING",
    invalidFrom: "QUEUED",
    call: (service, order) =>
      service.markRestaurantOrderAssembled(order, context),
    state: RestaurantOrderState.ASSEMBLED,
    published: undefined,
  },
  {
    rpc: "MarkRestaurantOrderReady",
    from: "ASSEMBLED",
    invalidFrom: "PREPARING",
    call: (service, order) => service.markRestaurantOrderReady(order, context),
    state: RestaurantOrderState.READY,
    published: "RestaurantOrderReady",
  },
  {
    rpc: "ReportPreparationDelay",
    from: "PREPARING",
    invalidFrom: "READY",
    call: (service, order, now) =>
      service.reportPreparationDelay(
        {
          ...order,
          expectedReadyAt: timestampFromDate(
            new Date(now.getTime() + minutes(PREPARATION_MINUTES + 15)),
          ),
          reason: "rush",
        },
        context,
      ),
    state: RestaurantOrderState.PREPARING,
    published: "PreparationDelayed",
  },
  {
    rpc: "ReportPreparationFailure",
    from: "PREPARING",
    invalidFrom: "QUEUED",
    call: (service, order) =>
      service.reportPreparationFailure(
        { ...order, reason: "ITEM_UNAVAILABLE" },
        context,
      ),
    state: RestaurantOrderState.FAILED,
    published: "RestaurantOrderFailed",
  },
];

/** Kitchen world called by restaurant staff, with a switchable caller. */
const createKitchenCallerWorld = () => {
  let caller: Subject = cook;
  const created = createWorld({
    subjects: { resolve: async () => caller },
  });
  return {
    ...created,
    callAs: (subject: Subject) => {
      caller = subject;
    },
  };
};

for (const kitchen of KITCHEN_RPCS) {
  test(`FR-480 ${kitchen.rpc} moves the order and publishes its fact`, async () => {
    const { world, service } = createKitchenCallerWorld();
    const { restaurantId, phoId } = await world.givenKitchen();
    const order = await world.givenOrderIn(kitchen.from, restaurantId, phoId);
    const before = world.outbox.eventTypes().length;

    const response = await kitchen.call(service, order, world.clock.now());

    assert.equal(
      response.restaurantOrder?.restaurantOrderId,
      order.restaurantOrderId,
    );
    assert.equal(response.restaurantOrder?.state, kitchen.state);
    assert.deepEqual(
      world.outbox.eventTypes().slice(before),
      kitchen.published === undefined ? [] : [kitchen.published],
    );
  });

  test(`FR-481 ${kitchen.rpc} without restaurant#operate_kitchen is PERMISSION_DENIED`, async () => {
    const { world, service, callAs } = createKitchenCallerWorld();
    const { restaurantId, phoId } = await world.givenKitchen();
    const order = await world.givenOrderIn(kitchen.from, restaurantId, phoId);
    const before = world.outbox.eventTypes().length;
    callAs({ type: "user", id: "stranger" });

    await assert.rejects(
      async () => kitchen.call(service, order, new Date()),
      rejectsWith(Code.PermissionDenied),
    );
    assert.equal(world.outbox.eventTypes().length, before);
  });

  test(`FR-482 ${kitchen.rpc} from ${kitchen.invalidFrom} is FAILED_PRECONDITION`, async () => {
    const { world, service } = createKitchenCallerWorld();
    const { restaurantId, phoId } = await world.givenKitchen();
    const order = await world.givenOrderIn(
      kitchen.invalidFrom,
      restaurantId,
      phoId,
    );
    const before = world.outbox.eventTypes().length;

    await assert.rejects(
      async () => kitchen.call(service, order, new Date()),
      rejectsWith(Code.FailedPrecondition),
    );
    assert.equal(world.outbox.eventTypes().length, before);
  });
}

test("AC-541 kitchen commands are UNAUTHENTICATED with the default rejecting resolver", async () => {
  const { world, service } = createWorld({ subjects: null });
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("RECEIVED", restaurantId, phoId);

  for (const kitchen of KITCHEN_RPCS) {
    await assert.rejects(
      async () => kitchen.call(service, order, new Date()),
      rejectsWith(Code.Unauthenticated),
    );
  }
});

test("AC-542 a kitchen command for another restaurant's order is NOT_FOUND", async () => {
  const { world, service } = createKitchenCallerWorld();
  const own = await world.givenKitchen("Pho Bar");
  const other = await world.givenKitchen("Noodle Bar");
  const order = await world.givenOrderIn(
    "RECEIVED",
    other.restaurantId,
    other.phoId,
  );

  await assert.rejects(
    async () =>
      service.acceptRestaurantOrder(
        {
          restaurantId: own.restaurantId,
          restaurantOrderId: order.restaurantOrderId,
        },
        context,
      ),
    rejectsWith(Code.NotFound),
  );
});

test("AC-543 kitchen commands validate required fields as INVALID_ARGUMENT", async () => {
  const { world, service } = createKitchenCallerWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("PREPARING", restaurantId, phoId);

  await assert.rejects(
    async () =>
      service.startPreparation(
        { restaurantId, restaurantOrderId: "" },
        context,
      ),
    rejectsWith(Code.InvalidArgument),
  );
  await assert.rejects(
    async () => service.reportPreparationDelay(order, context),
    rejectsWith(Code.InvalidArgument),
  );
  await assert.rejects(
    async () =>
      service.reportPreparationFailure({ ...order, reason: " " }, context),
    rejectsWith(Code.InvalidArgument),
  );
});

test("AC-544 repeated StartPreparation and MarkRestaurantOrderReady succeed without a new fact", async () => {
  const { world, service } = createKitchenCallerWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const preparing = await world.givenOrderIn("PREPARING", restaurantId, phoId);
  const ready = await world.givenOrderIn("READY", restaurantId, phoId);
  const before = world.outbox.eventTypes().length;

  const started = await service.startPreparation(preparing, context);
  const markedReady = await service.markRestaurantOrderReady(ready, context);

  assert.equal(started.restaurantOrder?.state, RestaurantOrderState.PREPARING);
  assert.equal(markedReady.restaurantOrder?.state, RestaurantOrderState.READY);
  assert.equal(world.outbox.eventTypes().length, before);
});

test("AC-545 AcceptRestaurantOrder answers the expected ready time", async () => {
  const { world, service } = createKitchenCallerWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const order = await world.givenOrderIn("RECEIVED", restaurantId, phoId);

  const response = await service.acceptRestaurantOrder(order, context);

  const expected = response.restaurantOrder?.expectedReadyAt;
  assert.ok(expected !== undefined);
  assert.ok(timestampDate(expected).getTime() > 0);
  assert.ok((response.restaurantOrder?.version ?? 0n) > 0n);
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
