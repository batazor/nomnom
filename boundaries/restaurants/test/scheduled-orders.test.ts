import assert from "node:assert/strict";
import test from "node:test";

import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Code } from "@connectrpc/connect";

import {
  OrderPriority,
  RestaurantOrderState,
} from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { PermissionDeniedError } from "../pkg/authz/errors.ts";
import {
  InvalidOrderScheduleError,
  InvalidRestaurantOrderError,
  InvalidRestaurantOrderReason,
} from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import { KitchenSimulator } from "../slices/simulator/applications/simulate-kitchen/simulate-kitchen.ts";
import {
  context,
  createWorld,
  rejectsWith,
  resolverOf,
} from "./support/grpc.ts";
import {
  cook,
  createKitchenWorld,
  minutes,
  ordersService,
} from "./support/kitchen.ts";

type World = Awaited<ReturnType<typeof createKitchenWorld>>;

/** Delivery Window and Planned Ready At `plannedMinutes` from now. */
const scheduleIn = (world: World, plannedMinutes: number) => {
  const plannedReadyAt = new Date(
    world.clock.now().getTime() + minutes(plannedMinutes),
  );
  const windowStart = new Date(plannedReadyAt.getTime() + minutes(20));
  return {
    windowStart,
    windowEnd: new Date(windowStart.getTime() + minutes(15)),
    plannedReadyAt,
  };
};

const commandOf = (restaurantId: string, restaurantOrderId: string) => ({
  subject: cook,
  restaurantId,
  restaurantOrderId,
});

test("AC-921 a scheduled order is received with its window and Planned Ready At", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const schedule = scheduleIn(world, 180);

  const received = await world.givenReceivedOrder(
    restaurantId,
    phoId,
    "STANDARD",
    schedule,
  );

  assert.equal(received.state, "RECEIVED");
  assert.deepEqual(received.schedule, schedule);
  const repeat = await world.receive.execute({
    subject: ordersService,
    restaurantId,
    orderId: received.orderId,
    items: [{ menuItemId: phoId, quantity: 2 }],
    priority: "STANDARD",
    schedule,
  });
  assert.equal(repeat.created, false);
  assert.equal(
    repeat.restaurantOrder.restaurantOrderId,
    received.restaurantOrderId,
    "orders queues the order by this id",
  );
  await assert.rejects(
    world.receive.execute({
      subject: ordersService,
      restaurantId,
      orderId: received.orderId,
      items: [{ menuItemId: phoId, quantity: 2 }],
      priority: "STANDARD",
    }),
    (error) =>
      error instanceof Error &&
      "code" in error &&
      error.code === "already_exists",
    "the same order without its window conflicts",
  );
});

test("FR-683 an incomplete schedule or Planned Ready At not before the window is invalid", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const schedule = scheduleIn(world, 180);
  for (const invalid of [
    { windowStart: schedule.windowStart, windowEnd: schedule.windowEnd },
    { ...schedule, plannedReadyAt: schedule.windowStart },
    { ...schedule, windowEnd: schedule.windowStart },
  ]) {
    await assert.rejects(
      world.receive.execute({
        subject: ordersService,
        restaurantId,
        orderId: "order-invalid",
        items: [{ menuItemId: phoId, quantity: 1 }],
        priority: "STANDARD",
        schedule: invalid,
      }),
      InvalidOrderScheduleError,
    );
  }
  assert.equal(
    await world.restaurantOrders.findByOrderId("order-invalid"),
    undefined,
  );
});

test("AC-922 AC-925 an accepted scheduled order waits in ACCEPTED, out of the Kitchen Queue", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const schedule = scheduleIn(world, 180);
  const received = await world.givenReceivedOrder(
    restaurantId,
    phoId,
    "PREMIUM",
    schedule,
  );
  const outboxBefore = (await world.outbox.records()).length;

  const accepted = await world.accept.execute(
    commandOf(restaurantId, received.restaurantOrderId),
  );

  assert.equal(accepted.state, "ACCEPTED");
  assert.deepEqual(accepted.timeline.expectedReadyAt, schedule.plannedReadyAt);
  assert.equal(accepted.timeline.queuedAt, undefined);
  const records = (await world.outbox.records()).slice(outboxBefore);
  assert.deepEqual(
    records.map((record) => record.event.type),
    ["RestaurantOrderAccepted"],
  );
  const event = records[0]?.event as unknown as {
    expectedReadyAt: Date;
    queuedAt?: Date;
  };
  assert.deepEqual(event.expectedReadyAt, schedule.plannedReadyAt);
  assert.equal(event.queuedAt, undefined, "FR-644 not queued yet");

  const scheduled = await world.listScheduledOrders.execute({
    subject: cook,
    restaurantId,
  });
  assert.deepEqual(scheduled.orders, [
    {
      restaurantOrderId: received.restaurantOrderId,
      orderId: received.orderId,
      items: [{ menuItemId: phoId, quantity: 2, options: [] }],
      schedule,
      priority: "PREMIUM",
      version: accepted.version,
    },
  ]);
  const queue = await world.getKitchenQueue.execute({
    subject: cook,
    restaurantId,
  });
  assert.deepEqual(queue.entries, [], "AC-925 not in the Kitchen Queue");
  await assert.rejects(
    world.listScheduledOrders.execute({ subject: ordersService, restaurantId }),
    PermissionDeniedError,
  );
});

test("AC-923 the kitchen names no ready time for a scheduled order", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const received = await world.givenReceivedOrder(
    restaurantId,
    phoId,
    "STANDARD",
    scheduleIn(world, 180),
  );

  await assert.rejects(
    world.accept.execute({
      ...commandOf(restaurantId, received.restaurantOrderId),
      expectedReadyAt: new Date(world.clock.now().getTime() + minutes(30)),
    }),
    (error) =>
      error instanceof InvalidRestaurantOrderError &&
      error.reason ===
        InvalidRestaurantOrderReason.ExpectedReadyAtOfScheduledOrder,
  );
  assert.equal(
    (await world.restaurantOrders.get(received.restaurantOrderId))?.state,
    "RECEIVED",
  );
});

test("AC-251 AC-252 FR-224 orders queues a scheduled order at its start; a repeat changes nothing", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const schedule = scheduleIn(world, 180);
  const received = await world.givenReceivedOrder(
    restaurantId,
    phoId,
    "STANDARD",
    schedule,
  );
  await world.accept.execute(
    commandOf(restaurantId, received.restaurantOrderId),
  );
  world.clock.advance(minutes(150));
  const outboxBefore = (await world.outbox.records()).length;

  const queued = await world.queue.execute({
    subject: ordersService,
    restaurantOrderId: received.restaurantOrderId,
  });
  const repeated = await world.queue.execute({
    subject: ordersService,
    restaurantOrderId: received.restaurantOrderId,
  });

  assert.equal(queued.state, "QUEUED");
  assert.deepEqual(queued.timeline.queuedAt, world.clock.now());
  assert.deepEqual(queued.timeline.expectedReadyAt, schedule.plannedReadyAt);
  assert.equal(repeated.version, queued.version);
  assert.equal((await world.outbox.records()).length, outboxBefore);
  const queue = await world.getKitchenQueue.execute({
    subject: cook,
    restaurantId,
  });
  assert.deepEqual(
    queue.entries.map((entry) => [entry.restaurantOrderId, entry.schedule]),
    [[received.restaurantOrderId, schedule]],
  );
  assert.deepEqual(
    (await world.listScheduledOrders.execute({ subject: cook, restaurantId }))
      .orders,
    [],
  );

  // FR-644: Preparation Started carries the queuing moment.
  const queuedAt = world.clock.now();
  world.clock.advance(minutes(1));
  await world.startNext(restaurantId);
  const started = (await world.outbox.records()).find(
    (record) => record.event.type === "PreparationStarted",
  )?.event as unknown as { queuedAt: Date } | undefined;
  assert.deepEqual(started?.queuedAt, queuedAt);
});

test("AC-924 kitchen staff cannot queue a scheduled order", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const received = await world.givenReceivedOrder(
    restaurantId,
    phoId,
    "STANDARD",
    scheduleIn(world, 180),
  );
  await world.accept.execute(
    commandOf(restaurantId, received.restaurantOrderId),
  );

  await assert.rejects(
    world.queue.execute({
      subject: cook,
      restaurantOrderId: received.restaurantOrderId,
    }),
    PermissionDeniedError,
  );
  assert.equal(
    (await world.restaurantOrders.get(received.restaurantOrderId))?.state,
    "ACCEPTED",
  );
});

test("AC-953 a queued scheduled order overtakes premium and aged standard orders", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const aged = await world.givenReceivedOrder(restaurantId, phoId, "STANDARD");
  await world.accept.execute(commandOf(restaurantId, aged.restaurantOrderId));
  const scheduled = await world.givenReceivedOrder(
    restaurantId,
    phoId,
    "STANDARD",
    scheduleIn(world, 120),
  );
  await world.accept.execute(
    commandOf(restaurantId, scheduled.restaurantOrderId),
  );
  // The standard order ages past 20 minutes × KITCHEN_QUEUE__AGING_FACTOR.
  world.clock.advance(minutes(45));
  const premium = await world.givenReceivedOrder(
    restaurantId,
    phoId,
    "PREMIUM",
  );
  await world.accept.execute(
    commandOf(restaurantId, premium.restaurantOrderId),
  );
  world.clock.advance(minutes(1));
  await world.queue.execute({
    subject: ordersService,
    restaurantOrderId: scheduled.restaurantOrderId,
  });

  const queue = await world.getKitchenQueue.execute({
    subject: cook,
    restaurantId,
  });

  assert.deepEqual(
    queue.entries.map((entry) => entry.restaurantOrderId),
    [
      scheduled.restaurantOrderId,
      aged.restaurantOrderId,
      premium.restaurantOrderId,
    ],
  );
  const started = await world.startNext(restaurantId);
  assert.equal(started?.restaurantOrderId, scheduled.restaurantOrderId);
});

test("AC-954 scheduled orders: premium first, then by Planned Ready At", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const standardEarly = await world.givenReceivedOrder(
    restaurantId,
    phoId,
    "STANDARD",
    scheduleIn(world, 100),
  );
  const premiumLate = await world.givenReceivedOrder(
    restaurantId,
    phoId,
    "PREMIUM",
    scheduleIn(world, 110),
  );
  const standardLater = await world.givenReceivedOrder(
    restaurantId,
    phoId,
    "STANDARD",
    scheduleIn(world, 105),
  );
  for (const order of [standardLater, premiumLate, standardEarly]) {
    await world.accept.execute(
      commandOf(restaurantId, order.restaurantOrderId),
    );
    await world.queue.execute({
      subject: ordersService,
      restaurantOrderId: order.restaurantOrderId,
    });
    world.clock.advance(minutes(1));
  }

  const queue = await world.getKitchenQueue.execute({
    subject: cook,
    restaurantId,
  });

  assert.deepEqual(
    queue.entries.map((entry) => entry.restaurantOrderId),
    [
      premiumLate.restaurantOrderId,
      standardEarly.restaurantOrderId,
      standardLater.restaurantOrderId,
    ],
  );
});

test("FR-224 FR-683 FR-684 scheduled orders over gRPC", async () => {
  const { world, service } = await createWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const schedule = scheduleIn(world, 180);
  const timestamps = {
    windowStart: timestampFromDate(schedule.windowStart),
    windowEnd: timestampFromDate(schedule.windowEnd),
    plannedReadyAt: timestampFromDate(schedule.plannedReadyAt),
  };

  const received = await service.receiveOrderRequest(
    {
      restaurantId,
      orderId: "order-grpc",
      items: [{ menuItemId: phoId, quantity: 1 }],
      priority: OrderPriority.STANDARD,
      schedule: timestamps,
    },
    context,
  );
  const repeated = await service.receiveOrderRequest(
    {
      restaurantId,
      orderId: "order-grpc",
      items: [{ menuItemId: phoId, quantity: 1 }],
      priority: OrderPriority.STANDARD,
      schedule: timestamps,
    },
    context,
  );
  assert.deepEqual(
    [repeated.restaurantOrderId, repeated.created],
    [received.restaurantOrderId, false],
  );
  await assert.rejects(
    service.receiveOrderRequest(
      {
        restaurantId,
        orderId: "order-partial",
        items: [{ menuItemId: phoId, quantity: 1 }],
        priority: OrderPriority.STANDARD,
        schedule: { windowStart: timestamps.windowStart },
      },
      context,
    ),
    rejectsWith(Code.InvalidArgument),
  );
  await world.accept.execute(
    commandOf(restaurantId, received.restaurantOrderId),
  );

  const staff = await createWorld({ subjects: resolverOf(cook) });
  await assert.rejects(
    staff.service.queueRestaurantOrder(
      { restaurantOrderId: received.restaurantOrderId },
      context,
    ),
    rejectsWith(Code.PermissionDenied),
  );

  const queued = await service.queueRestaurantOrder(
    { restaurantOrderId: received.restaurantOrderId },
    context,
  );
  assert.equal(queued.restaurantOrder?.state, RestaurantOrderState.QUEUED);
  assert.deepEqual(
    queued.restaurantOrder?.schedule?.plannedReadyAt === undefined
      ? undefined
      : timestampDate(queued.restaurantOrder.schedule.plannedReadyAt),
    schedule.plannedReadyAt,
  );
  await assert.rejects(
    service.queueRestaurantOrder({ restaurantOrderId: "" }, context),
    rejectsWith(Code.InvalidArgument),
  );
});

test("FR-684 ListScheduledOrders over gRPC requires operate_kitchen", async () => {
  const { world, service } = await createWorld({ subjects: resolverOf(cook) });
  const { restaurantId, phoId } = await world.givenKitchen();
  const schedule = scheduleIn(world, 180);
  const received = await world.givenReceivedOrder(
    restaurantId,
    phoId,
    "PREMIUM",
    schedule,
  );
  await world.accept.execute(
    commandOf(restaurantId, received.restaurantOrderId),
  );

  const response = await service.listScheduledOrders({ restaurantId }, context);

  assert.deepEqual(
    response.orders.map((order) => [
      order.restaurantOrderId,
      order.orderId,
      order.priority,
      order.schedule?.windowStart === undefined
        ? undefined
        : timestampDate(order.schedule.windowStart),
    ]),
    [
      [
        received.restaurantOrderId,
        received.orderId,
        OrderPriority.PREMIUM,
        schedule.windowStart,
      ],
    ],
  );
  const fulfillment = await createWorld({
    subjects: resolverOf(ordersService),
  });
  await assert.rejects(
    fulfillment.service.listScheduledOrders({ restaurantId }, context),
    rejectsWith(Code.PermissionDenied),
  );
});

test("the Simulator accepts a scheduled order without a time of its own", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const schedule = scheduleIn(world, 180);
  const received = await world.givenReceivedOrder(
    restaurantId,
    phoId,
    "STANDARD",
    schedule,
  );
  const simulator = new KitchenSimulator({
    state: {
      isEnabled: async () => true,
      setEnabled: async () => {},
    },
    orders: world.board,
    owners: { ownerOf: async () => cook.id },
    commands: {
      accept: world.accept,
      reject: world.reject,
      startPreparation: world.startPreparation,
      markAssembled: world.markAssembled,
      markReady: world.markReady,
      reportDelay: world.reportDelay,
      reportFailure: world.reportFailure,
    },
    clock: world.clock,
    // Always "accept" (the first 90 percent) with no delay.
    random: () => 0,
    delay: { minMs: 0, maxMs: 0 },
    log: (message, fields) =>
      assert.fail(`${message} ${String(fields?.error)}`),
  });

  await simulator.tick();
  await simulator.tick();

  const accepted = await world.restaurantOrders.get(received.restaurantOrderId);
  assert.equal(accepted?.state, "ACCEPTED");
  assert.deepEqual(accepted?.timeline.expectedReadyAt, schedule.plannedReadyAt);
  assert.equal(simulator.planOf(received.restaurantOrderId), undefined);
});
