import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";

import {
  type DataPoint,
  type Histogram,
  MeterProvider,
  MetricReader,
} from "@opentelemetry/sdk-metrics";
import type { MongoClient } from "mongodb";

import { mongoPoolOptions } from "../pkg/infrastructure/mongodb/client.ts";
import { MongoPoolMetrics } from "../pkg/infrastructure/mongodb/pool-metrics.ts";

class TestReader extends MetricReader {
  protected async onShutdown(): Promise<void> {}
  protected async onForceFlush(): Promise<void> {}
}

const POOL = "db.client.connection.pool.name";
const STATE = "db.client.connection.state";
const PRIMARY = "mongo-0:27017";
const SECONDARY = "mongo-1:27017";

const points = async (
  reader: TestReader,
  name: string,
): Promise<DataPoint<unknown>[]> => {
  const { resourceMetrics } = await reader.collect();
  return resourceMetrics.scopeMetrics
    .flatMap((scope) => scope.metrics)
    .filter((metric) => metric.descriptor.name === name)
    .flatMap((metric) => metric.dataPoints as DataPoint<unknown>[]);
};

/** Value per pool (and state) of a gauge or counter. */
const values = async (reader: TestReader, name: string) =>
  Object.fromEntries(
    (await points(reader, name)).map((point) => [
      [point.attributes[POOL], point.attributes[STATE]]
        .filter((part) => part !== undefined)
        .join(" "),
      point.value as number,
    ]),
  );

const histogram = async (reader: TestReader, name: string, pool: string) => {
  const point = (await points(reader, name)).find(
    (candidate) => candidate.attributes[POOL] === pool,
  );
  return point?.value as Histogram | undefined;
};

const setup = () => {
  const reader = new TestReader();
  const provider = new MeterProvider({ readers: [reader] });
  let now = 0;
  const metrics = new MongoPoolMetrics({
    meter: provider.getMeter("test"),
    poolName: (address) => `${address}/restaurants`,
    now: () => now,
  });
  const client = new EventEmitter();
  metrics.instrument(client as unknown as MongoClient);
  const emit = (name: string, event: Record<string, unknown>) =>
    client.emit(name, { address: PRIMARY, time: new Date(), ...event });
  return {
    reader,
    emit,
    advance: (ms: number) => {
      now += ms;
    },
  };
};

const created = (emit: ReturnType<typeof setup>["emit"]) =>
  emit("connectionPoolCreated", {
    options: { maxPoolSize: 20, minPoolSize: 0, maxConnecting: 2 },
  });

test("pool gauges follow checkout and check-in per server", async () => {
  const { reader, emit, advance } = setup();
  created(emit);
  emit("connectionPoolCreated", {
    address: SECONDARY,
    options: { maxPoolSize: 20, minPoolSize: 1 },
  });
  for (const connectionId of [1, 2, 3]) {
    emit("connectionCheckOutStarted", {});
    emit("connectionCreated", { connectionId });
    emit("connectionReady", { connectionId, durationMS: 12 });
    emit("connectionCheckedOut", { connectionId, durationMS: 15 });
  }
  emit("connectionCheckOutStarted", {});
  // Monitoring connections are outside the pools.
  emit("connectionCreated", { connectionId: "<monitor>" });
  advance(40);
  emit("connectionCheckedIn", { connectionId: 1 });

  const pool = `${PRIMARY}/restaurants`;
  const secondary = `${SECONDARY}/restaurants`;
  assert.deepEqual(await values(reader, "db.client.connection.count"), {
    [`${pool} used`]: 2,
    [`${pool} idle`]: 1,
    [`${secondary} used`]: 0,
    [`${secondary} idle`]: 0,
  });
  assert.deepEqual(await values(reader, "db.client.connection.max"), {
    [pool]: 20,
    [secondary]: 20,
  });
  assert.deepEqual(await values(reader, "db.client.connection.idle.min"), {
    [pool]: 0,
    [secondary]: 1,
  });
  assert.deepEqual(
    await values(reader, "db.client.connection.pending_requests"),
    { [pool]: 1, [secondary]: 0 },
  );

  const wait = await histogram(reader, "db.client.connection.wait_time", pool);
  assert.equal(wait?.count, 3);
  assert.ok(Math.abs((wait?.sum ?? 0) - 0.045) < 1e-9);
  const create = await histogram(
    reader,
    "db.client.connection.create_time",
    pool,
  );
  assert.equal(create?.count, 3);
  const use = await histogram(reader, "db.client.connection.use_time", pool);
  assert.equal(use?.count, 1);
  assert.ok(Math.abs((use?.sum ?? 0) - 0.04) < 1e-9);
});

test("checkout timeouts count only waitQueueTimeoutMS failures", async () => {
  const { reader, emit } = setup();
  created(emit);
  emit("connectionCheckOutStarted", {});
  emit("connectionCheckOutFailed", { reason: "timeout", durationMS: 5000 });
  emit("connectionCheckOutStarted", {});
  emit("connectionCheckOutFailed", {
    reason: "connectionError",
    durationMS: 3,
  });

  const pool = `${PRIMARY}/restaurants`;
  assert.deepEqual(await values(reader, "db.client.connection.timeouts"), {
    [pool]: 1,
  });
  assert.deepEqual(
    await values(reader, "db.client.connection.pending_requests"),
    { [pool]: 0 },
  );
  const wait = await histogram(reader, "db.client.connection.wait_time", pool);
  assert.equal(wait?.count, 2);
});

test("pool clears are counted and closed connections leave the pool", async () => {
  const { reader, emit } = setup();
  created(emit);
  emit("connectionCreated", { connectionId: 1 });
  emit("connectionCheckedOut", { connectionId: 1, durationMS: 1 });
  emit("connectionPoolCleared", { interruptInUseConnections: true });
  emit("connectionClosed", { connectionId: 1, reason: "stale" });

  const pool = `${PRIMARY}/restaurants`;
  assert.deepEqual(
    await values(reader, "nomnom.db.client.connection.pool.clears"),
    { [pool]: 1 },
  );
  assert.deepEqual(await values(reader, "db.client.connection.count"), {
    [`${pool} used`]: 0,
    [`${pool} idle`]: 0,
  });
  // A check-in after the close does not resurrect the connection.
  emit("connectionCheckedIn", { connectionId: 1 });
  assert.equal(
    await histogram(reader, "db.client.connection.use_time", pool),
    undefined,
  );
});

test("a closed pool stops reporting", async () => {
  const { reader, emit } = setup();
  created(emit);
  emit("connectionPoolClosed", {});
  assert.deepEqual(await values(reader, "db.client.connection.max"), {});
});

test("pool config maps to MongoClient options", () => {
  assert.deepEqual(
    mongoPoolOptions({
      maxPoolSize: 3,
      minPoolSize: 0,
      maxIdleTimeMs: 300_000,
      waitQueueTimeoutMs: 5000,
      maxConnecting: 2,
    }),
    {
      maxPoolSize: 3,
      minPoolSize: 0,
      maxIdleTimeMS: 300_000,
      waitQueueTimeoutMS: 5000,
      maxConnecting: 2,
    },
  );
});
