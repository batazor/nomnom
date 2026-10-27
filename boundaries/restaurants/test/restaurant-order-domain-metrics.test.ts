import assert from "node:assert/strict";
import test from "node:test";

import { MeterProvider, MetricReader } from "@opentelemetry/sdk-metrics";

import type { UnitOfWork } from "../pkg/application/unit-of-work.ts";
import { DatabaseInbox } from "../pkg/infrastructure/repository/inbox.ts";
import {
  CancellationRejectionReason,
  type CancelRestaurantOrderResult,
} from "../slices/restaurantorder/applications/cancel-restaurant-order/cancel-restaurant-order.ts";
import { RestaurantOrder } from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import {
  cancellationRejection,
  ObservedOutbox,
  ObservedRestaurantOrderRepository,
  ObservedUnitOfWork,
  observeReceiveOrderRequest,
  observeTransitionRejections,
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

const setup = async () => {
  const reader = new TestReader();
  const provider = new MeterProvider({ readers: [reader] });
  const metrics = new RestaurantOrderDomainMetrics({
    meter: provider.getMeter("test"),
  });
  const infrastructure = await createInfrastructure();
  const unitOfWork = new ObservedUnitOfWork(infrastructure.unitOfWork, metrics);
  const outbox = new ObservedOutbox(infrastructure.outbox, metrics);
  const world = await createKitchenWorld({
    infrastructure,
    unitOfWork,
    outbox,
  });
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
  const { reader, world, receive } = await setup();
  const { restaurantId, phoId } = await world.givenKitchen();
  const request = (orderId: string) => ({
    subject: ordersService,
    restaurantId,
    orderId,
    items: [{ menuItemId: phoId, quantity: 1 }],
    priority: "STANDARD",
  });
  // Started first: the Kitchen Queue takes its head, not a named order.
  const preparing = await world.givenOrderIn("PREPARING", restaurantId, phoId);
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
  const { reader, unitOfWork, outbox } = await setup();

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
  const { reader, unitOfWork, outbox } = await setup();

  await unitOfWork.run(async () => {
    await unitOfWork.run(() =>
      outbox.add(aggregate("ro-1"), [accepted("ro-1")]),
    );
    assert.deepEqual(await sums(reader, ACCEPTED), {}, "not before commit");
  });

  assert.deepEqual(await sums(reader, ACCEPTED), { "": 1 });
});

test("a retried transaction attempt is counted once", async () => {
  const { reader, metrics } = await setup();
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
  const { reader, infrastructure, world, unitOfWork, receive } = await setup();
  const { restaurantId, phoId } = await world.givenKitchen();
  const inbox = new DatabaseInbox(infrastructure.db, infrastructure.unitOfWork);
  const record = {
    consumer: "restaurants.order-placed",
    eventId: "event-1",
    eventType: "nomnom.orders.v1.OrderPlaced",
    aggregateId: "order-a",
    topic: "orders.v1.order-events",
  };
  // The process Inbox runs on the observed Unit of Work (ObservedDatabaseUnitOfWork).
  const deliver = () =>
    unitOfWork.run(() =>
      inbox.runOnce(record, async () => {
        await receive.execute({
          subject: ordersService,
          restaurantId,
          orderId: "order-a",
          items: [{ menuItemId: phoId, quantity: 1 }],
          priority: "STANDARD",
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
    priority: "STANDARD",
  });

  assert.equal(repeat.created, false);
  assert.deepEqual(await sums(reader, RECEIVED), { "": 1 });
});

test("a received order inside a rolled back Inbox transaction is not counted", async () => {
  const { reader, world, unitOfWork, receive } = await setup();
  const { restaurantId, phoId } = await world.givenKitchen();

  await assert.rejects(
    unitOfWork.run(async () => {
      await receive.execute({
        subject: ordersService,
        restaurantId,
        orderId: "order-a",
        items: [{ menuItemId: phoId, quantity: 1 }],
        priority: "STANDARD",
      });
      throw new Error("inbox commit failed");
    }),
  );

  assert.deepEqual(await sums(reader, RECEIVED), {});
});

test("an unknown or missing reason is recorded as other", async () => {
  const { reader, unitOfWork, outbox } = await setup();
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
    outbox.add(aggregate("ro-1"), [rejected("Kitchen on fire")]),
  );
  await unitOfWork.run(() =>
    outbox.add(aggregate("ro-2"), [rejected(undefined)]),
  );
  await unitOfWork.run(() =>
    outbox.add(aggregate("ro-3"), [rejected("too_busy")]),
  );
  // Events of other aggregates are not Restaurant Order transitions.
  await unitOfWork.run(() =>
    outbox.add({ type: "Restaurant", id: "r", version: 1 }, [rejected("x")]),
  );

  assert.deepEqual(await sums(reader, REJECTED), { other: 2, too_busy: 1 });
});

const TRANSITIONS_METRIC = "nomnom.fsm.transitions";

/** `from>trigger>to result terminal` of every nomnom.fsm.transitions series. */
const transitions = async (
  reader: TestReader,
): Promise<Record<string, number>> => {
  const { resourceMetrics } = await reader.collect();
  const result: Record<string, number> = {};
  for (const scope of resourceMetrics.scopeMetrics) {
    for (const metric of scope.metrics) {
      if (metric.descriptor.name !== TRANSITIONS_METRIC) continue;
      assert.equal(metric.descriptor.unit, "{transition}");
      for (const point of metric.dataPoints) {
        const a = point.attributes;
        assert.equal(a["nomnom.fsm.aggregate_type"], "restaurant_order");
        const key = `${a["nomnom.fsm.from_state"]}>${a["nomnom.fsm.trigger"]}>${a["nomnom.fsm.to_state"]} ${a["nomnom.fsm.result"]} ${a["nomnom.fsm.terminal"]}`;
        result[key] = (result[key] ?? 0) + Number(point.value);
      }
    }
  }
  return result;
};

const fsmSetup = () => {
  const reader = new TestReader();
  const provider = new MeterProvider({ readers: [reader] });
  const metrics = new RestaurantOrderDomainMetrics({
    meter: provider.getMeter("test"),
  });
  const saved: RestaurantOrder[] = [];
  const repository = new ObservedRestaurantOrderRepository(
    {
      get: async () => undefined,
      findByOrderId: async () => undefined,
      save: async (order) => {
        saved.push(order);
        return order.version + 1;
      },
    },
    metrics,
  );
  return { reader, metrics, repository, saved };
};

const at = (minute: number) => new Date(Date.UTC(2026, 9, 25, 9, minute));
const eligible = { satisfied: true, reasons: [] };
const newOrder = (id: string) =>
  RestaurantOrder.receive({
    id,
    orderId: `order-${id}`,
    restaurantId: "r-1",
    marketId: "berlin",
    items: [{ menuItemId: "pho", quantity: 1 }],
    priority: "STANDARD",
    receivedAt: at(0),
  });

test("saved Restaurant Orders record their applied FSM transitions in order", async () => {
  const { reader, repository } = fsmSetup();
  const order = newOrder("ro-1");
  // Creation is not a transition.
  await repository.save(order);
  order.pullEvents();
  assert.deepEqual(await transitions(reader), {});

  // Accept and Queue in one save, as Accept Order does.
  order.accept({
    acceptedAt: at(1),
    expectedReadyAt: at(30),
    eligibility: eligible,
  });
  order.queue({ queuedAt: at(1) });
  await repository.save(order);
  order.pullEvents();
  order.startPreparation({
    startedAt: at(2),
    cookId: "cook-1",
    requestId: "start-1",
  });
  order.reportDelay({ reportedAt: at(3), expectedReadyAt: at(40) });
  await repository.save(order);
  order.pullEvents();
  order.markAssembled({ assembledAt: at(4) });
  order.markReady({ readyAt: at(5) });
  await repository.save(order);
  const cancelled = newOrder("ro-2");
  cancelled.pullEvents();
  cancelled.cancel({
    cancelledAt: at(1),
    policy: { duringPreparation: "reject" },
  });
  await repository.save(cancelled);

  assert.deepEqual(await transitions(reader), {
    "received>accept>accepted applied false": 1,
    "accepted>queue>queued applied false": 1,
    "queued>start_preparation>preparing applied false": 1,
    "preparing>report_delay>preparing applied false": 1,
    "preparing>mark_assembled>assembled applied false": 1,
    "assembled>mark_ready>ready applied true": 1,
    "received>cancel>cancelled applied true": 1,
  });
});

test("FSM transitions inside a Unit of Work are recorded only after it commits", async () => {
  const { reader, metrics, repository } = fsmSetup();
  const unitOfWork = new ObservedUnitOfWork({ run: (work) => work() }, metrics);
  const rejected = newOrder("ro-1");
  rejected.pullEvents();
  rejected.reject({ rejectedAt: at(1), reason: "too_busy" });
  const failed = newOrder("ro-2");
  failed.pullEvents();
  failed.accept({
    acceptedAt: at(1),
    expectedReadyAt: at(30),
    eligibility: eligible,
  });

  await assert.rejects(
    unitOfWork.run(async () => {
      await repository.save(failed);
      throw new Error("abort");
    }),
    /abort/,
  );
  await unitOfWork.run(async () => {
    await repository.save(rejected);
    assert.deepEqual(await transitions(reader), {}, "not before commit");
  });

  assert.deepEqual(await transitions(reader), {
    "received>reject>rejected applied true": 1,
  });
});

test("an invalid transition and a cancellation of a ready order are rejected transitions", async () => {
  const { reader, metrics } = fsmSetup();
  const order = newOrder("ro-1");
  const markReady = observeTransitionRejections(
    {
      execute: async (_command: unknown): Promise<never> => {
        order.markReady({ readyAt: at(1) });
        assert.fail("RECEIVED does not allow MarkReady");
      },
    },
    metrics,
  );
  const cancel = observeTransitionRejections(
    {
      execute: async (
        _command: unknown,
      ): Promise<CancelRestaurantOrderResult> => ({
        outcome: "cancellation_rejected",
        restaurantOrderId: "ro-2",
        reason: CancellationRejectionReason.AlreadyReady,
        state: "READY",
      }),
    },
    metrics,
    cancellationRejection,
  );
  const guarded = observeTransitionRejections(
    {
      execute: async (
        _command: unknown,
      ): Promise<CancelRestaurantOrderResult> => ({
        outcome: "cancellation_rejected",
        restaurantOrderId: "ro-3",
        reason:
          CancellationRejectionReason.CancellationDuringPreparationNotAllowed,
        state: "PREPARING",
      }),
    },
    metrics,
    cancellationRejection,
  );

  await assert.rejects(markReady.execute(undefined as never), {
    name: "InvalidTransitionError",
  });
  await cancel.execute(undefined as never);
  // A guard rejection is not a missing transition.
  await guarded.execute(undefined as never);

  assert.deepEqual(await transitions(reader), {
    "received>mark_ready>received rejected false": 1,
    "ready>cancel>ready rejected true": 1,
  });
});
