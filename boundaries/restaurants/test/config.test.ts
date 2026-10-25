import assert from "node:assert/strict";
import test from "node:test";

import { loadConfig } from "../pkg/config/config.ts";

test("uses the default port", () => {
  assert.equal(loadConfig({}).server.port, 8082);
});

test("SERVER__PORT takes precedence over PORT", () => {
  const config = loadConfig({
    SERVER__PORT: "9002",
    PORT: "9001",
  });

  assert.equal(config.server.port, 9002);
});

test("rejects an invalid port", () => {
  assert.throws(() => loadConfig({ PORT: "not-a-port" }));
  assert.throws(() => loadConfig({ PORT: "70000" }));
});

test("uses default management port and shutdown budget", () => {
  const config = loadConfig({});

  assert.equal(config.management.port, 9082);
  assert.deepEqual(config.shutdown, {
    readinessDelayMs: 5000,
    drainTimeoutMs: 20000,
  });
});

test("reads shutdown budget from the environment", () => {
  const config = loadConfig({
    SHUTDOWN__READINESS_DELAY: "0s",
    SHUTDOWN__DRAIN_TIMEOUT: "1500ms",
  });

  assert.deepEqual(config.shutdown, {
    readinessDelayMs: 0,
    drainTimeoutMs: 1500,
  });
});

test("rejects an invalid shutdown budget", () => {
  assert.throws(() => loadConfig({ SHUTDOWN__DRAIN_TIMEOUT: "0s" }));
  assert.throws(() => loadConfig({ SHUTDOWN__READINESS_DELAY: "5" }));
});

test("rejects a management port equal to the server port", () => {
  assert.throws(() => loadConfig({ MANAGEMENT__PORT: "8082" }));
});

test("MongoDB is disabled without MONGODB__URI", () => {
  assert.deepEqual(loadConfig({}).mongodb, { enabled: false });
  assert.deepEqual(loadConfig({ MONGODB__URI: "  " }).mongodb, {
    enabled: false,
  });
});

test("MongoDB config defaults the database and timeout", () => {
  assert.deepEqual(
    loadConfig({ MONGODB__URI: "mongodb://mongo:27017/?replicaSet=rs0" })
      .mongodb,
    {
      enabled: true,
      uri: "mongodb://mongo:27017/?replicaSet=rs0",
      database: "restaurants",
      timeoutMs: 5000,
      pool: {
        maxPoolSize: 20,
        minPoolSize: 0,
        maxIdleTimeMs: 300_000,
        waitQueueTimeoutMs: 5000,
        maxConnecting: 2,
      },
    },
  );
});

test("MongoDB pool defaults depend on the process", () => {
  const environment = { MONGODB__URI: "mongodb://m" };
  const maxPoolSize = (process?: "api" | "relay" | "consumer" | "migrate") => {
    const { mongodb } = loadConfig(environment, { process });
    assert.ok(mongodb.enabled);
    return [mongodb.pool.maxPoolSize, mongodb.pool.maxConnecting];
  };
  assert.deepEqual(maxPoolSize(), [20, 2]);
  assert.deepEqual(maxPoolSize("relay"), [3, 2]);
  assert.deepEqual(maxPoolSize("consumer"), [5, 2]);
  assert.deepEqual(maxPoolSize("migrate"), [2, 2]);
});

test("MongoDB pool reads MONGODB__* overrides", () => {
  const { mongodb } = loadConfig({
    MONGODB__URI: "mongodb://m",
    MONGODB__MAX_POOL_SIZE: "8",
    MONGODB__MIN_POOL_SIZE: "1",
    MONGODB__MAX_IDLE_TIME: "1m",
    MONGODB__WAIT_QUEUE_TIMEOUT: "750ms",
    MONGODB__MAX_CONNECTING: "4",
  });
  assert.ok(mongodb.enabled);
  assert.deepEqual(mongodb.pool, {
    maxPoolSize: 8,
    minPoolSize: 1,
    maxIdleTimeMs: 60_000,
    waitQueueTimeoutMs: 750,
    maxConnecting: 4,
  });
  // The default maxConnecting never exceeds a one-connection pool.
  const single = loadConfig({
    MONGODB__URI: "mongodb://m",
    MONGODB__MAX_POOL_SIZE: "1",
  }).mongodb;
  assert.ok(single.enabled);
  assert.equal(single.pool.maxConnecting, 1);
});

test("MongoDB pool rejects values out of range at startup", () => {
  const invalid: Record<string, string>[] = [
    { MONGODB__MAX_POOL_SIZE: "0" },
    { MONGODB__MAX_POOL_SIZE: "101" },
    { MONGODB__MAX_POOL_SIZE: "2.5" },
    { MONGODB__MAX_POOL_SIZE: "many" },
    { MONGODB__MIN_POOL_SIZE: "-1" },
    { MONGODB__MAX_POOL_SIZE: "5", MONGODB__MIN_POOL_SIZE: "6" },
    { MONGODB__MAX_IDLE_TIME: "0s" },
    { MONGODB__MAX_IDLE_TIME: "300000" },
    { MONGODB__WAIT_QUEUE_TIMEOUT: "0ms" },
    { MONGODB__MAX_CONNECTING: "0" },
    { MONGODB__MAX_POOL_SIZE: "3", MONGODB__MAX_CONNECTING: "4" },
  ];
  for (const overrides of invalid) {
    assert.throws(
      () => loadConfig({ MONGODB__URI: "mongodb://m", ...overrides }),
      /MONGODB__/,
      JSON.stringify(overrides),
    );
  }
});

test("MongoDB config rejects malformed values", () => {
  assert.throws(() => loadConfig({ MONGODB__URI: "http://mongo" }));
  assert.throws(() =>
    loadConfig({ MONGODB__URI: "mongodb://m", MONGODB__DATABASE: "a/b" }),
  );
  assert.throws(() =>
    loadConfig({ MONGODB__URI: "mongodb://m", MONGODB__TIMEOUT: "0s" }),
  );
});

test("durations accept hours and days", () => {
  const config = loadConfig({ OUTBOX__RETENTION: "36h" });
  assert.equal(config.outbox.retentionMs, 36 * 3_600_000);
  assert.equal(
    loadConfig({ RECONCILER__GRACE: "1d" }).reconciler.graceMs,
    86_400_000,
  );
  assert.throws(() => loadConfig({ OUTBOX__RETENTION: "7w" }));
});

test("Kafka is disabled without KAFKA__BROKERS and the relay has defaults", () => {
  const config = loadConfig({});
  assert.deepEqual(config.kafka, { enabled: false });
  assert.deepEqual(config.outbox, {
    retentionMs: 7 * 86_400_000,
    pollIntervalMs: 1000,
    batchSize: 100,
    leaseTtlMs: 15_000,
  });
});

test("Kafka config parses brokers and rejects malformed ones", () => {
  assert.deepEqual(
    loadConfig({ KAFKA__BROKERS: " kafka-0:9092, kafka-1:9092 " }).kafka,
    {
      enabled: true,
      brokers: ["kafka-0:9092", "kafka-1:9092"],
      clientId: "restaurants-relay",
    },
  );
  assert.throws(() => loadConfig({ KAFKA__BROKERS: "kafka-0" }));
  assert.throws(() => loadConfig({ KAFKA__BROKERS: "a:1,,b:2" }));
});

test("Outbox relay config rejects a lease shorter than the poll interval", () => {
  assert.throws(() =>
    loadConfig({ OUTBOX__LEASE_TTL: "1s", OUTBOX__POLL_INTERVAL: "2s" }),
  );
  assert.throws(() => loadConfig({ OUTBOX__BATCH_SIZE: "0" }));
});

test("the consumer reads its group from the oldest offset with bounded retries by default", () => {
  assert.deepEqual(loadConfig({}).consumer, {
    groupId: "restaurants",
    fromBeginning: true,
    maxRetries: 5,
    retryInitialIntervalMs: 100,
    retryMaxIntervalMs: 5000,
  });
  assert.deepEqual(
    loadConfig({
      KAFKA__CONSUMER_GROUP: "restaurants-test",
      KAFKA__INITIAL_OFFSET: "newest",
      CONSUMER__MAX_RETRIES: "0",
      CONSUMER__RETRY_INITIAL_INTERVAL: "1s",
      CONSUMER__RETRY_MAX_INTERVAL: "1m",
    }).consumer,
    {
      groupId: "restaurants-test",
      fromBeginning: false,
      maxRetries: 0,
      retryInitialIntervalMs: 1000,
      retryMaxIntervalMs: 60_000,
    },
  );
});

test("the consumer config rejects malformed values", () => {
  assert.throws(() => loadConfig({ KAFKA__CONSUMER_GROUP: "bad group" }));
  assert.throws(() => loadConfig({ KAFKA__INITIAL_OFFSET: "latest" }));
  assert.throws(() => loadConfig({ CONSUMER__MAX_RETRIES: "-1" }));
  assert.throws(() =>
    loadConfig({
      CONSUMER__RETRY_INITIAL_INTERVAL: "10s",
      CONSUMER__RETRY_MAX_INTERVAL: "1s",
    }),
  );
});

test("kitchen tunables default to the MVP decisions", () => {
  assert.deepEqual(loadConfig({}).kitchen, {
    preparationTimeMs: 20 * 60_000,
    repeatPolicy: "succeed-without-event",
    cancellationDuringPreparation: "reject",
  });
});

test("kitchen tunables are read from the environment", () => {
  assert.deepEqual(
    loadConfig({
      KITCHEN__PREPARATION_TIME: "45m",
      KITCHEN__REPEAT_POLICY: "reject",
      KITCHEN__CANCELLATION_DURING_PREPARATION: "allow",
    }).kitchen,
    {
      preparationTimeMs: 45 * 60_000,
      repeatPolicy: "reject",
      cancellationDuringPreparation: "allow",
    },
  );
});

test("kitchen tunables reject malformed values", () => {
  assert.throws(() => loadConfig({ KITCHEN__PREPARATION_TIME: "0m" }));
  assert.throws(() => loadConfig({ KITCHEN__PREPARATION_TIME: "20" }));
  assert.throws(
    () => loadConfig({ KITCHEN__REPEAT_POLICY: "ignore" }),
    /KITCHEN__REPEAT_POLICY/,
  );
  assert.throws(
    () => loadConfig({ KITCHEN__CANCELLATION_DURING_PREPARATION: "maybe" }),
    /KITCHEN__CANCELLATION_DURING_PREPARATION/,
  );
});
