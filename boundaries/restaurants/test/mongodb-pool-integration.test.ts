import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";

import {
  type DataPoint,
  type Histogram,
  MeterProvider,
  MetricReader,
} from "@opentelemetry/sdk-metrics";
import { MongoError } from "mongodb";

import {
  connectMongoDb,
  type MongoDbConnection,
} from "../pkg/infrastructure/repository/client.ts";
import { mongoUri } from "./support/mongodb.ts";

// Runs against a MongoDB replica set, for example
// the testcontainer of the global setup (test/support/global-containers.ts).
const uri = mongoUri();

class TestReader extends MetricReader {
  protected async onShutdown(): Promise<void> {}
  protected async onForceFlush(): Promise<void> {}
}

const POOL = "db.client.connection.pool.name";

describe("MongoDB connection pool metrics", () => {
  const reader = new TestReader();
  const provider = new MeterProvider({ readers: [reader] });
  const database = `restaurants_pool_${randomUUID().slice(0, 8)}`;
  let mongo: MongoDbConnection;

  const points = async (name: string) => {
    const { resourceMetrics } = await reader.collect();
    return resourceMetrics.scopeMetrics
      .flatMap((scope) => scope.metrics)
      .filter((metric) => metric.descriptor.name === name)
      .flatMap((metric) => metric.dataPoints as DataPoint<unknown>[]);
  };

  before(async () => {
    mongo = await connectMongoDb(
      {
        enabled: true,
        uri,
        database,
        timeoutMs: 5000,
        pool: {
          maxPoolSize: 1,
          minPoolSize: 0,
          maxIdleTimeMs: 60_000,
          waitQueueTimeoutMs: 200,
          maxConnecting: 1,
        },
      },
      { meter: provider.getMeter("test") },
    );
    await mongo.db.collection("probe").insertOne({ _id: "probe" } as never);
  });

  after(async () => {
    if (mongo === undefined) return;
    await mongo.db.dropDatabase();
    await mongo.close();
  });

  test("reports max, usage and checkout timeouts of the single-connection pool", async () => {
    const max = await points("db.client.connection.max");
    assert.equal(max.length, 1, "one pool for the directConnection server");
    assert.equal(max[0]?.value, 1);
    const pool = String(max[0]?.attributes[POOL]);
    assert.match(pool, /:\d+\/restaurants_pool_/);

    // A slow query holds the only connection; the second checkout times out
    // after waitQueueTimeoutMS instead of waiting forever.
    const slow = mongo.db
      .collection("probe")
      .find({ $where: "sleep(1000) || true" })
      .toArray();
    await new Promise((resolve) => setTimeout(resolve, 100));
    const used = (await points("db.client.connection.count")).find(
      (point) => point.attributes["db.client.connection.state"] === "used",
    );
    assert.equal(used?.value, 1);
    await assert.rejects(
      mongo.db.collection("probe").findOne({ _id: "probe" } as never),
      // WaitQueueTimeoutError is not exported by the driver.
      (error) =>
        error instanceof MongoError &&
        /checking out a connection/.test(error.message),
    );
    await slow;

    const timeouts = await points("db.client.connection.timeouts");
    assert.equal(timeouts[0]?.value, 1);
    const wait = (await points("db.client.connection.wait_time"))[0]?.value as
      | Histogram
      | undefined;
    assert.ok((wait?.count ?? 0) >= 3);
    assert.ok((wait?.max ?? 0) >= 0.15, "the timed-out checkout waited");
    const use = (await points("db.client.connection.use_time"))[0]?.value as
      | Histogram
      | undefined;
    assert.ok((use?.max ?? 0) >= 0.9, "the slow query held its connection");
    const create = (await points("db.client.connection.create_time"))[0]
      ?.value as Histogram | undefined;
    assert.equal(create?.count, 1);
    const pending = await points("db.client.connection.pending_requests");
    assert.equal(pending[0]?.value, 0);
  });
});
