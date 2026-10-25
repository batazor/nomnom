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
    },
  );
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
