import assert from "node:assert/strict";
import test from "node:test";

import { MeterProvider, MetricReader } from "@opentelemetry/sdk-metrics";

import type { UnitOfWork } from "../pkg/application/unit-of-work.ts";
import { InMemoryInbox } from "../pkg/infrastructure/memory/inbox.ts";
import {
  ObservedOutbox,
  ObservedUnitOfWork,
  observeReceiveOrderRequest,
  RestaurantOrderDomainMetrics,
} from "../slices/restaurantorder/infrastructure/observability/domain-metrics.ts";
import {
  cook,
  createKitchenWorld,
  minutes,
  ordersService,
} from "./support/kitchen.ts";
import { createInfrastructure } from "./support/restaurants.ts";

class TestReader extends MetricReader {
  protected async onShutdown(): Promise<void> {}
  protected async onForceFlush(): Promise<void> {}
}

/** Sum of a counter per `nomnom.reason` ("" without the attribute). */
const sums = async (
  reader: TestReader,
  name: string,
): Promise<Record<string, number>> => {
  const { resourceMetrics } = await reader.collect();
  const result: Record<string, number> = {};
  for (const scope of resourceMetrics.scopeMetrics) {
    for (const metric of scope.metrics) {
      if (metric.descriptor.name !== name) continue;
      assert.equal(
        metric.descriptor.unit,
        name === "nomnom.kitchen.delays" ? "{delay}" : "{order}",
      );
      for (const point of metric.dataPoints) {
        const key = String(point.attributes["nomnom.reason"] ?? "");
        result[key] = (result[key] ?? 0) + Number(point.value);
      }
    }
  }
  return result;
};

const RECEIVED = "nomnom.restaurants.orders.received";
const ACCEPTED = "nomnom.restaurants.orders.accepted";
const REJECTED = "nomnom.restaurants.orders.rejected";
const DELAYS = "nomnom.kitchen.delays";

const setup = () => {
  const reader = new TestReader();
  const provider = new MeterProvider({ readers: [reader] });
  const metrics = new RestaurantOrderDomainMetrics({
    meter: provider.getMeter("test"),
  });
  const infrastructure = createInfrastructure();
  const unitOfWork = new ObservedUnitOfWork(infrastructure.unitOfWork, metrics);
  const outbox = new ObservedOutbox(infrastructure.outbox, metrics);
  const world = createKitchenWorld({ infrastructure, unitOfWork, outbox });
  const receive = observeReceiveOrderRequest(world.receive, metrics);
  return {
    reader,
    metrics,
    infrastructure,
    world,
    unitOfWork,
    outbox,
    receive,
  };
};

const accepted = (orderId: string) => ({
  type: "RestaurantOrderAccepted",
  restaurantOrderId: orderId,
  orderId,
  restaurantId: "r",
  occurredAt: new Date(),
  acceptedAt: new Date(),
  expectedReadyAt: new Date(),
});
const aggregate = (id: string) => ({ type: "RestaurantOrder", id, version: 2 });

test("committed kitchen commands count received, accepted, rejected and delays", async () => {
  const { reader, world, receive } = setup();
  const { restaurantId, phoId } = await world.givenKitchen();
  const request = (orderId: string) => ({
    subject: ordersService,
    restaurantId,
    orderId,
    items: [{ menuItemId: phoId, quantity: 1 }],
  });
  const first = await receive.execute(request("order-a"));
  const second = await receive.execute(request("order-b"));
  await world.accept.execute({
    subject: cook,
    restaurantId,
    restaurantOrderId: first.restaurantOrder.restaurantOrderId,
  });
  await world.reject.execute({
    subject: cook,
    restaurantId,
    restaurantOrderId: second.restaurantOrder.restaurantOrderId,
    reason: "too_busy",
  });
  const preparing = await world.givenOrderIn("PREPARING", restaurantId, phoId);
  await world.reportDelay.execute({
    ...preparing,
    expectedReadyAt: new Date(world.clock.now().getTime() + minutes(90)),
    reason: "item_unavailable",
  });

  assert.deepEqual(await sums(reader, RECEIVED), { "": 2 });
  // givenOrderIn accepts through the observed Unit of Work as well.
  assert.deepEqual(await sums(reader, ACCEPTED), { "": 2 });
  assert.deepEqual(await sums(reader, REJECTED), { too_busy: 1 });
  assert.deepEqual(await sums(reader, DELAYS), { item_unavailable: 1 });
});

test("a rolled back Unit of Work records nothing", async () => {
  const { reader, unitOfWork, outbox } = setup();

  await assert.rejects(
    unitOfWork.run(async () => {
      await outbox.add(aggregate("ro-1"), [accepted("ro-1")]);
      throw new Error("abort");
    }),
    /abort/,
  );

  assert.deepEqual(await sums(reader, ACCEPTED), {});
});

test("a nested Unit of Work records its events once, after the outer commit", async () => {
  const { reader, unitOfWork, outbox } = setup();

  await unitOfWork.run(async () => {
    await unitOfWork.run(() =>
      outbox.add(aggregate("ro-1"), [accepted("ro-1")]),
    );
    assert.deepEqual(await sums(reader, ACCEPTED), {}, "not before commit");
  });

  assert.deepEqual(await sums(reader, ACCEPTED), { "": 1 });
});

test("a retried transaction attempt is counted once", async () => {
  const { reader, metrics } = setup();
  // Like the MongoDB driver on TransientTransactionError: `work` runs again.
  const retrying: UnitOfWork = {
    async run(work) {
      await work();
      return work();
    },
  };
  const unitOfWork = new ObservedUnitOfWork(retrying, metrics);
  const outbox = new ObservedOutbox({ add: async () => {} }, metrics);

  await unitOfWork.run(() => outbox.add(aggregate("ro-1"), [accepted("ro-1")]));

  assert.deepEqual(await sums(reader, ACCEPTED), { "": 1 });
});

test("an idempotent repeat and an Inbox duplicate record nothing more", async () => {
  const { reader, infrastructure, world, unitOfWork, receive } = setup();
  const { restaurantId, phoId } = await world.givenKitchen();
  const inbox = new InMemoryInbox(infrastructure.unitOfWork);
  const record = {
    consumer: "restaurants.order-placed",
    eventId: "event-1",
    eventType: "nomnom.orders.v1.OrderPlaced",
    aggregateId: "order-a",
    topic: "orders.v1.order-events",
  };
  // The process Inbox runs on the observed Unit of Work (ObservedMongoUnitOfWork).
  const deliver = () =>
    unitOfWork.run(() =>
      inbox.runOnce(record, async () => {
        await receive.execute({
          subject: ordersService,
          restaurantId,
          orderId: "order-a",
          items: [{ menuItemId: phoId, quantity: 1 }],
        });
      }),
    );

  assert.equal(await deliver(), "processed");
  assert.equal(await deliver(), "duplicate");
  const repeat = await receive.execute({
    subject: ordersService,
    restaurantId,
    orderId: "order-a",
    items: [{ menuItemId: phoId, quantity: 1 }],
  });

  assert.equal(repeat.created, false);
  assert.deepEqual(await sums(reader, RECEIVED), { "": 1 });
});

test("a received order inside a rolled back Inbox transaction is not counted", async () => {
  const { reader, world, unitOfWork, receive } = setup();
  const { restaurantId, phoId } = await world.givenKitchen();

  await assert.rejects(
    unitOfWork.run(async () => {
      await receive.execute({
        subject: ordersService,
        restaurantId,
        orderId: "order-a",
        items: [{ menuItemId: phoId, quantity: 1 }],
      });
      throw new Error("inbox commit failed");
    }),
  );

  assert.deepEqual(await sums(reader, RECEIVED), {});
});

test("an unknown or missing reason is recorded as other", async () => {
  const { reader, unitOfWork, outbox } = setup();
  const rejected = (reason: string | undefined) => ({
    type: "RestaurantOrderRejected",
    restaurantOrderId: "ro",
    orderId: "o",
    restaurantId: "r",
    occurredAt: new Date(),
    stage: "RECEIVED",
    reason,
  });

  await unitOfWork.run(() =>
    outbox.add(aggregate("ro-1"), [
      rejected("Kitchen on fire"),
      rejected(undefined),
      rejected("too_busy"),
    ]),
  );
  // Events of other aggregates are not Restaurant Order transitions.
  await unitOfWork.run(() =>
    outbox.add({ type: "Restaurant", id: "r", version: 1 }, [rejected("x")]),
  );

  assert.deepEqual(await sums(reader, REJECTED), { other: 2, too_busy: 1 });
});
