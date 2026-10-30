import assert from "node:assert/strict";
import test from "node:test";

import { create, toBinary } from "@bufbuild/protobuf";
import { type Timestamp, timestampFromDate } from "@bufbuild/protobuf/wkt";

import {
  PreparationStartedSchema,
  RestaurantOrderAcceptedSchema,
  RestaurantOrderCancelledSchema,
  RestaurantOrderReadySchema,
  RestaurantWaitLevelChangedSchema,
} from "../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import type { Envelope } from "../infrastructure/messaging/envelope.ts";
import type { EventHandler } from "../infrastructure/messaging/processor.ts";
import { DatabaseOutbox } from "../infrastructure/repository/outbox.ts";
import {
  RecordWaitTimeHandler,
  WAIT_TIME_AGGREGATE_TYPE,
} from "../slices/restaurant/applications/record-wait-time/record-wait-time.ts";
import { restaurantWaitTimeEventMapper } from "../slices/restaurant/infrastructure/integration/restaurant-events.ts";
import { createWaitTimeHandlers } from "../slices/restaurant/infrastructure/integration/wait-time-facts.ts";
import {
  DatabaseRestaurantWaitTimes,
  type RestaurantDocument,
} from "../slices/restaurant/infrastructure/repository/restaurants.ts";
import { WaitTimeReadyTimeEstimator } from "../slices/restaurantorder/infrastructure/acceptance/wait-time.ts";
import {
  createRestaurantWorld,
  owner,
  testWaitTimePolicy,
} from "./support/restaurants.ts";

const MINUTE = 60_000;

const worldOf = async () => {
  const world = await createRestaurantWorld();
  const store = new DatabaseRestaurantWaitTimes(world.store);
  const recordWaitTime = new RecordWaitTimeHandler({
    unitOfWork: world.unitOfWork,
    store,
    policy: testWaitTimePolicy(),
    outbox: world.outbox,
    clock: world.clock,
  });
  const handlers = new Map(
    createWaitTimeHandlers({ recordWaitTime }).map((handler) => [
      handler.eventType,
      handler,
    ]),
  );
  /** Delivers a fact like the consumer: inside the Unit of Work of its Inbox. */
  const deliver = async (
    schema:
      | typeof RestaurantOrderAcceptedSchema
      | typeof PreparationStartedSchema
      | typeof RestaurantOrderReadySchema
      | typeof RestaurantOrderCancelledSchema,
    payload: { orderId: string; restaurantId: string; queuedAt?: Timestamp },
    occurredAt: Date,
  ) => {
    const handler = handlers.get(schema.typeName) as EventHandler;
    const envelope: Envelope = {
      eventId: `${schema.typeName}:${payload.orderId}`,
      eventType: schema.typeName,
      occurredAt,
      aggregateType: "RestaurantOrder",
      aggregateId: payload.orderId,
      aggregateVersion: 1,
    };
    await world.unitOfWork.run(() =>
      handler.handle(
        envelope,
        toBinary(
          schema,
          create(schema as typeof RestaurantOrderAcceptedSchema, payload),
        ),
      ),
    );
  };
  const waitTimeOf = async (restaurantId: string) =>
    (
      (await world.db
        .collection<RestaurantDocument>("restaurants")
        .findOne({ _id: restaurantId })) as RestaurantDocument
    ).waitTime;
  return { ...world, store, deliver, waitTimeOf };
};

test("AC-837 AC-838 a ready order records its duration once", async () => {
  const world = await worldOf();
  const restaurantId = await world.givenRestaurant("Pho");
  const start = world.clock.now();
  const fact = { orderId: "order-1", restaurantId };

  await world.deliver(
    RestaurantOrderAcceptedSchema,
    { ...fact, queuedAt: timestampFromDate(start) },
    start,
  );
  assert.deepEqual(
    (await world.waitTimeOf(restaurantId))?.open.map((open) => open.orderId),
    ["order-1"],
  );
  const readyAt = new Date(start.getTime() + 18 * MINUTE);
  await world.deliver(RestaurantOrderReadySchema, fact, readyAt);
  await world.deliver(RestaurantOrderReadySchema, fact, readyAt);

  const waitTime = await world.waitTimeOf(restaurantId);
  assert.deepEqual(waitTime?.open, []);
  assert.deepEqual(
    waitTime?.completed.map((entry) => entry.durationMs),
    [18 * MINUTE],
  );
});

test("AC-839 a cancelled order leaves without a duration", async () => {
  const world = await worldOf();
  const restaurantId = await world.givenRestaurant("Pho");
  const fact = { orderId: "order-1", restaurantId };
  await world.deliver(
    RestaurantOrderAcceptedSchema,
    { ...fact, queuedAt: timestampFromDate(world.clock.now()) },
    world.clock.now(),
  );
  await world.deliver(RestaurantOrderCancelledSchema, fact, world.clock.now());

  const waitTime = await world.waitTimeOf(restaurantId);
  assert.deepEqual(waitTime?.open, []);
  assert.deepEqual(waitTime?.completed, []);
});

test("FR-656 a fact without restaurant_id changes nothing", async () => {
  const world = await worldOf();
  const restaurantId = await world.givenRestaurant("Pho");
  await world.deliver(
    RestaurantOrderAcceptedSchema,
    {
      orderId: "order-1",
      restaurantId: "",
      queuedAt: timestampFromDate(world.clock.now()),
    },
    world.clock.now(),
  );
  assert.equal(await world.waitTimeOf(restaurantId), undefined);
});

test("AC-840 restaurant saves keep the statistics and conflict with nothing", async () => {
  const world = await worldOf();
  const restaurantId = await world.givenRestaurant("Pho");
  const before = await world.restaurants.get(restaurantId);
  await world.deliver(
    RestaurantOrderAcceptedSchema,
    {
      orderId: "order-1",
      restaurantId,
      queuedAt: timestampFromDate(world.clock.now()),
    },
    world.clock.now(),
  );

  const closed = await world.close.execute({ subject: owner, restaurantId });

  assert.equal(closed.version, (before?.version ?? 0) + 1);
  assert.deepEqual(
    (await world.waitTimeOf(restaurantId))?.open.map((open) => open.orderId),
    ["order-1"],
  );
});

test("AC-844 AC-845 a level change is published once, other facts publish nothing", async () => {
  const world = await worldOf();
  const restaurantId = await world.givenRestaurant("Pho");
  const recorded = async () =>
    (await world.outbox.records()).filter(
      (record) => record.aggregate.type === WAIT_TIME_AGGREGATE_TYPE,
    );
  await world.deliver(
    RestaurantOrderAcceptedSchema,
    {
      orderId: "order-1",
      restaurantId,
      queuedAt: timestampFromDate(world.clock.now()),
    },
    world.clock.now(),
  );
  assert.deepEqual(await recorded(), [], "no change of the level");

  // The order waits 35 minutes against a 20 minute baseline.
  world.clock.advance(35 * MINUTE);
  await world.deliver(
    RestaurantOrderAcceptedSchema,
    {
      orderId: "order-2",
      restaurantId,
      queuedAt: timestampFromDate(world.clock.now()),
    },
    world.clock.now(),
  );
  await world.deliver(
    RestaurantOrderAcceptedSchema,
    {
      orderId: "order-3",
      restaurantId,
      queuedAt: timestampFromDate(world.clock.now()),
    },
    world.clock.now(),
  );

  const changes = await recorded();
  assert.equal(changes.length, 1);
  assert.deepEqual(changes[0]?.aggregate, {
    type: WAIT_TIME_AGGREGATE_TYPE,
    id: restaurantId,
    version: 1,
  });
  assert.equal(
    (changes[0]?.event as unknown as { elevated: boolean } | undefined)
      ?.elevated,
    true,
  );
  assert.equal((await world.waitTimeOf(restaurantId))?.level, "ELEVATED");
  const view = await world.catalog.get(restaurantId);
  assert.deepEqual(view?.waitTime, {
    elevated: true,
    expectedWaitMinutes: 35,
    baselinePreparationMs: 20 * MINUTE,
  });
});

test("FR-646 RestaurantWaitLevelChanged keeps distinct Outbox records per change", async () => {
  const world = await worldOf();
  const restaurantId = await world.givenRestaurant("Pho");
  const outbox = new DatabaseOutbox(world.db, world.unitOfWork, {
    mapper: restaurantWaitTimeEventMapper,
  });
  const recordWaitTime = new RecordWaitTimeHandler({
    unitOfWork: world.unitOfWork,
    store: world.store,
    policy: testWaitTimePolicy(),
    outbox,
    clock: world.clock,
  });
  const fact = (orderId: string, kind: "queued" | "ready") =>
    recordWaitTime.execute({
      restaurantId,
      orderId,
      fact: kind,
      occurredAt: world.clock.now(),
    });
  await fact("late", "queued");
  world.clock.advance(35 * MINUTE);
  await fact("other", "queued");
  await fact("late", "ready");
  await fact("other", "ready");

  const records = await world.db
    .collection("outbox")
    .find({ eventType: RestaurantWaitLevelChangedSchema.typeName })
    .sort({ aggregateVersion: 1 })
    .toArray();
  assert.deepEqual(
    records.map((record) => Number(record.aggregateVersion)),
    [1, 2],
  );
  assert.equal(records[0]?.topic, "restaurants.v1.restaurant-events");
  assert.equal(records[0]?.aggregateId, restaurantId);
});

test("AC-846 the ready time of an accepted order is the current wait ahead", async () => {
  const world = await worldOf();
  const restaurantId = await world.givenRestaurant("Pho");
  await world.deliver(
    RestaurantOrderAcceptedSchema,
    {
      orderId: "order-1",
      restaurantId,
      queuedAt: timestampFromDate(world.clock.now()),
    },
    world.clock.now(),
  );
  world.clock.advance(35 * MINUTE);
  const estimator = new WaitTimeReadyTimeEstimator({
    store: world.store,
    policy: testWaitTimePolicy(),
  });
  const acceptedAt = world.clock.now();

  const expectedReadyAt = await world.unitOfWork.run(() =>
    estimator.estimate({ restaurantId, acceptedAt }),
  );

  assert.equal(expectedReadyAt.getTime() - acceptedAt.getTime(), 35 * MINUTE);
});

test("AC-926 a scheduled order counts from its queuing, not from its Acceptance", async () => {
  const world = await worldOf();
  const restaurantId = await world.givenRestaurant("Pho");
  const fact = { orderId: "order-scheduled", restaurantId };
  const at = (hh: number, mm: number) =>
    new Date(Date.UTC(2026, 9, 28, hh - 1, mm));

  // Accepted at 15:00 without queued_at: it waits for its start in ACCEPTED.
  await world.deliver(RestaurantOrderAcceptedSchema, fact, at(15, 0));
  assert.equal(await world.waitTimeOf(restaurantId), undefined);
  const policy = testWaitTimePolicy();
  const before = await world.store.load(restaurantId);
  assert.equal(
    before === undefined
      ? undefined
      : policy.estimate(before.statistics, at(18, 5)).currentMs,
    20 * MINUTE,
    "before 18:10 the order does not change the current wait",
  );

  // Queued at 18:10 publishes nothing; Preparation Started carries it.
  await world.deliver(
    PreparationStartedSchema,
    { ...fact, queuedAt: timestampFromDate(at(18, 10)) },
    at(18, 11),
  );
  await world.deliver(
    PreparationStartedSchema,
    { ...fact, queuedAt: timestampFromDate(at(18, 10)) },
    at(18, 11),
  );
  assert.deepEqual((await world.waitTimeOf(restaurantId))?.open, [
    { orderId: "order-scheduled", queuedAt: at(18, 10) },
  ]);
  await world.deliver(RestaurantOrderReadySchema, fact, at(18, 35));

  const waitTime = await world.waitTimeOf(restaurantId);
  assert.deepEqual(waitTime?.open, []);
  assert.deepEqual(
    waitTime?.completed.map((entry) => entry.durationMs),
    [25 * MINUTE],
  );
});

test("FR-644 Preparation Started of an order opened at its Acceptance changes nothing", async () => {
  const world = await worldOf();
  const restaurantId = await world.givenRestaurant("Pho");
  const start = world.clock.now();
  const fact = { orderId: "order-1", restaurantId };
  await world.deliver(
    RestaurantOrderAcceptedSchema,
    { ...fact, queuedAt: timestampFromDate(start) },
    start,
  );
  await world.deliver(
    PreparationStartedSchema,
    { ...fact, queuedAt: timestampFromDate(start) },
    new Date(start.getTime() + 5 * MINUTE),
  );
  assert.deepEqual((await world.waitTimeOf(restaurantId))?.open, [
    { orderId: "order-1", queuedAt: start },
  ]);
  await assert.rejects(
    world.deliver(
      PreparationStartedSchema,
      { orderId: "order-2", restaurantId },
      start,
    ),
    /PreparationStarted requires queued_at/,
  );
});
