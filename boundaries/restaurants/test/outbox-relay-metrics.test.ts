import assert from "node:assert/strict";
import test from "node:test";

import { MeterProvider, MetricReader } from "@opentelemetry/sdk-metrics";

import { OUTBOX_TOPICS } from "../di/relay.ts";
import { OtelRelayMetrics } from "../pkg/infrastructure/outbox/metrics.ts";

class TestReader extends MetricReader {
  protected async onShutdown(): Promise<void> {}
  protected async onForceFlush(): Promise<void> {}
}

const DESTINATION = "messaging.destination.name";

/** Last value of a gauge per topic. */
const gauge = async (
  reader: TestReader,
  name: string,
): Promise<Record<string, number>> => {
  const { resourceMetrics } = await reader.collect();
  const result: Record<string, number> = {};
  for (const scope of resourceMetrics.scopeMetrics) {
    for (const metric of scope.metrics) {
      if (metric.descriptor.name !== name) continue;
      for (const point of metric.dataPoints) {
        result[String(point.attributes[DESTINATION])] = Number(point.value);
      }
    }
  }
  return result;
};

const setup = (knownTopics?: readonly string[]) => {
  const reader = new TestReader();
  const provider = new MeterProvider({ readers: [reader] });
  const metrics = new OtelRelayMetrics("restaurants", {
    meter: provider.getMeter("test"),
    ...(knownTopics === undefined ? {} : { knownTopics }),
  });
  return { reader, metrics };
};

test("known topics are reported at 0 while their backlog is empty", async () => {
  const { reader, metrics } = setup(OUTBOX_TOPICS);
  metrics.backlog([
    {
      stream: "restaurants.v1.menu-events",
      pending: 3,
      oldestOccurredAt: new Date(Date.now() - 10_000),
    },
  ]);

  const pending = await gauge(reader, "nomnom.outbox.pending.messages");
  assert.deepEqual(pending, {
    "restaurants.v1.menu-events": 3,
    "restaurants.v1.restaurant-events": 0,
    "restaurants.v1.restaurant-order-events": 0,
  });
  const oldest = await gauge(reader, "nomnom.outbox.oldest_pending.age");
  assert.equal(oldest["restaurants.v1.restaurant-events"], 0);
  assert.ok((oldest["restaurants.v1.menu-events"] ?? 0) >= 10);
});

test("a topic seen before drops to 0 instead of disappearing", async () => {
  const { reader, metrics } = setup();
  metrics.backlog([
    { stream: "x.v1.other", pending: 2, oldestOccurredAt: new Date() },
  ]);
  assert.deepEqual(await gauge(reader, "nomnom.outbox.pending.messages"), {
    "x.v1.other": 2,
  });

  metrics.backlog([]);

  assert.deepEqual(await gauge(reader, "nomnom.outbox.pending.messages"), {
    "x.v1.other": 0,
  });
  assert.deepEqual(await gauge(reader, "nomnom.outbox.oldest_pending.age"), {
    "x.v1.other": 0,
  });
});

test("a standby relay reports no backlog at all", async () => {
  const { reader, metrics } = setup(OUTBOX_TOPICS);
  metrics.backlog([]);
  metrics.standby();
  assert.deepEqual(await gauge(reader, "nomnom.outbox.pending.messages"), {});
});
