import assert from "node:assert/strict";
import test from "node:test";

import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Code } from "@connectrpc/connect";

import { RestaurantOrderState } from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import type { Subject } from "../pkg/application/subject.ts";
import { context, createWorld, rejectsWith } from "./support/grpc.ts";
import {
  cook,
  type createKitchenWorld,
  minutes,
  PREPARATION_MINUTES,
} from "./support/kitchen.ts";

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
