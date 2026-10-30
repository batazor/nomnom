import assert from "node:assert/strict";
import test from "node:test";

import {
  database,
  loadConfig,
  loadKafkaConfig,
  loadScheduledSlotConfig,
  loadSchemaRegistryConfig,
} from "../pkg/config/config.ts";

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

test("reads shutdown budget from the environment", () => {
  const config = loadConfig({
    SHUTDOWN__READINESS_DELAY: "0s",
    SHUTDOWN__DRAIN_TIMEOUT: "1500ms",
    SHUTDOWN__TELEMETRY_FLUSH_TIMEOUT: "2s",
  });

  assert.deepEqual(config.shutdown, {
    readinessDelayMs: 0,
    drainTimeoutMs: 1500,
    telemetryFlushTimeoutMs: 2000,
  });
});

test("rejects an invalid shutdown budget", () => {
  assert.throws(() => loadConfig({ SHUTDOWN__DRAIN_TIMEOUT: "0s" }));
  assert.throws(() => loadConfig({ SHUTDOWN__READINESS_DELAY: "5" }));
  assert.throws(() => loadConfig({ SHUTDOWN__TELEMETRY_FLUSH_TIMEOUT: "0s" }));
});

test("rejects a management port equal to the server port", () => {
  assert.throws(() => loadConfig({ MANAGEMENT__PORT: "8082" }));
});

test("MongoDB is required: an empty MONGODB__URI stops startup", () => {
  assert.throws(() => database.load({}), /MONGODB__URI is required/);
  assert.throws(
    () => database.load({ MONGODB__URI: "  " }),
    /MONGODB__URI is required/,
  );
});

test("MongoDB config defaults the database and timeout", () => {
  assert.deepEqual(
    database.load({ MONGODB__URI: "mongodb://mongo:27017/?replicaSet=rs0" }),
    {
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
  const maxPoolSize = (process?: "api" | "consumer") => {
    const { pool } = database.load(environment, process);
    return [pool.maxPoolSize, pool.maxConnecting];
  };
  assert.deepEqual(maxPoolSize(), [20, 2]);
  assert.deepEqual(maxPoolSize("consumer"), [5, 2]);
});

test("MongoDB pool reads MONGODB__* overrides", () => {
  const mongodb = database.load({
    MONGODB__URI: "mongodb://m",
    MONGODB__MAX_POOL_SIZE: "8",
    MONGODB__MIN_POOL_SIZE: "1",
    MONGODB__MAX_IDLE_TIME: "1m",
    MONGODB__WAIT_QUEUE_TIMEOUT: "750ms",
    MONGODB__MAX_CONNECTING: "4",
  });
  assert.deepEqual(mongodb.pool, {
    maxPoolSize: 8,
    minPoolSize: 1,
    maxIdleTimeMs: 60_000,
    waitQueueTimeoutMs: 750,
    maxConnecting: 4,
  });
  // The default maxConnecting never exceeds a one-connection pool.
  const single = database.load({
    MONGODB__URI: "mongodb://m",
    MONGODB__MAX_POOL_SIZE: "1",
  });
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
      () => database.load({ MONGODB__URI: "mongodb://m", ...overrides }),
      /MONGODB__/,
      JSON.stringify(overrides),
    );
  }
});

test("MongoDB config rejects malformed values", () => {
  assert.throws(() => database.load({ MONGODB__URI: "http://mongo" }));
  assert.throws(() =>
    database.load({ MONGODB__URI: "mongodb://m", MONGODB__DATABASE: "a/b" }),
  );
  assert.throws(() =>
    database.load({ MONGODB__URI: "mongodb://m", MONGODB__TIMEOUT: "0s" }),
  );
});

test("durations accept hours and days", () => {
  assert.equal(
    loadConfig({ RECONCILER__INTERVAL: "36h" }).reconciler.intervalMs,
    36 * 3_600_000,
  );
  assert.equal(
    loadConfig({ RECONCILER__GRACE: "1d" }).reconciler.graceMs,
    86_400_000,
  );
  assert.throws(() => loadConfig({ RECONCILER__GRACE: "7w" }));
});

test("Kafka is required: an empty KAFKA__BROKERS stops startup", () => {
  assert.throws(() => loadKafkaConfig({}), /KAFKA__BROKERS is required/);
});

test("Kafka config parses brokers and rejects malformed ones", () => {
  assert.deepEqual(
    loadKafkaConfig({ KAFKA__BROKERS: " kafka-0:9092, kafka-1:9092 " }),
    {
      brokers: ["kafka-0:9092", "kafka-1:9092"],
      clientId: "restaurants-consumer",
    },
  );
  assert.throws(() => loadKafkaConfig({ KAFKA__BROKERS: "kafka-0" }));
  assert.throws(() => loadKafkaConfig({ KAFKA__BROKERS: "a:1,,b:2" }));
});

test("FR-719: the consumer requires an http(s) Schema Registry url", () => {
  assert.throws(
    () => loadSchemaRegistryConfig({}),
    /SCHEMA_REGISTRY__URL is required/,
  );
  assert.throws(() =>
    loadSchemaRegistryConfig({ SCHEMA_REGISTRY__URL: "kafka:8081" }),
  );
  const config = loadSchemaRegistryConfig({
    SCHEMA_REGISTRY__URL: "http://kafka:8081",
  });
  assert.equal(config.url.href, "http://kafka:8081/");
  assert.equal(config.timeoutMs, 5_000);
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
    queueAgingFactor: 2,
  });
});

test("kitchen tunables are read from the environment", () => {
  assert.deepEqual(
    loadConfig({
      KITCHEN__PREPARATION_TIME: "45m",
      KITCHEN__REPEAT_POLICY: "reject",
      KITCHEN__CANCELLATION_DURING_PREPARATION: "allow",
      KITCHEN_QUEUE__AGING_FACTOR: "3.5",
    }).kitchen,
    {
      preparationTimeMs: 45 * 60_000,
      repeatPolicy: "reject",
      cancellationDuringPreparation: "allow",
      queueAgingFactor: 3.5,
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
  for (const factor of ["0", "-1", "soon"]) {
    assert.throws(
      () => loadConfig({ KITCHEN_QUEUE__AGING_FACTOR: factor }),
      /KITCHEN_QUEUE__AGING_FACTOR/,
    );
  }
});

test("FR-657 reads the wait time parameters and rejects inverted thresholds", () => {
  assert.deepEqual(loadConfig({}).waitTime, {
    sampleSize: 50,
    recentWindowMs: 30 * 60_000,
    elevatedFactor: 1.5,
    normalFactor: 1.2,
  });
  assert.equal(
    loadConfig({ WAIT_TIME__RECENT_WINDOW: "1h" }).waitTime.recentWindowMs,
    3_600_000,
  );
  assert.throws(() =>
    loadConfig({
      WAIT_TIME__ELEVATED_FACTOR: "1.2",
      WAIT_TIME__NORMAL_FACTOR: "1.2",
    }),
  );
  assert.throws(() => loadConfig({ WAIT_TIME__SAMPLE_SIZE: "0" }));
  assert.throws(() => loadConfig({ WAIT_TIME__ELEVATED_FACTOR: "fast" }));
});

/** SCHEDULED_SLOT__* of .env.example. */
const scheduledSlotEnvironment = {
  SCHEDULED_SLOT__WIDTH: "15m",
  SCHEDULED_SLOT__MIN_LEAD: "45m",
  SCHEDULED_SLOT__MAX_HORIZON: "48h",
  SCHEDULED_SLOT__HOLD_TTL: "10m",
  SCHEDULED_SLOT__ALTERNATIVES: "3",
};

test("FR-694 reads the Delivery Window grid from SCHEDULED_SLOT__*", () => {
  assert.deepEqual(loadScheduledSlotConfig(scheduledSlotEnvironment), {
    widthMs: 15 * 60_000,
    minLeadMs: 45 * 60_000,
    maxHorizonMs: 48 * 3_600_000,
    holdTtlMs: 10 * 60_000,
    alternatives: 3,
  });
});

test("FR-694 every SCHEDULED_SLOT__ value is required", () => {
  for (const name of Object.keys(scheduledSlotEnvironment)) {
    for (const value of [undefined, "", " "]) {
      assert.throws(
        () =>
          loadScheduledSlotConfig({
            ...scheduledSlotEnvironment,
            [name]: value,
          }),
        new RegExp(`${name} is required`),
      );
    }
  }
});

test("AC-937 a width that does not divide 30 minutes stops startup", () => {
  for (const width of ["20m", "7m", "45m", "1h"]) {
    assert.throws(
      () =>
        loadScheduledSlotConfig({
          ...scheduledSlotEnvironment,
          SCHEDULED_SLOT__WIDTH: width,
        }),
      /SCHEDULED_SLOT__WIDTH must divide 30 minutes/,
    );
  }
  for (const width of ["5m", "10m", "15m", "30m"]) {
    assert.equal(
      loadScheduledSlotConfig({
        ...scheduledSlotEnvironment,
        SCHEDULED_SLOT__WIDTH: width,
      }).widthMs % 60_000,
      0,
    );
  }
});

test("FR-694 malformed Delivery Window values stop startup", () => {
  for (const [name, value, message] of [
    ["SCHEDULED_SLOT__MIN_LEAD", "48h", /MIN_LEAD must be shorter/],
    ["SCHEDULED_SLOT__MIN_LEAD", "49h", /MIN_LEAD must be shorter/],
    ["SCHEDULED_SLOT__HOLD_TTL", "0m", /must be positive/],
    ["SCHEDULED_SLOT__WIDTH", "15", /expected a duration/],
    ["SCHEDULED_SLOT__ALTERNATIVES", "-1", /SCHEDULED_SLOT__ALTERNATIVES/],
    ["SCHEDULED_SLOT__ALTERNATIVES", "two", /SCHEDULED_SLOT__ALTERNATIVES/],
    ["SCHEDULED_SLOT__ALTERNATIVES", "1.5", /SCHEDULED_SLOT__ALTERNATIVES/],
  ] as const) {
    assert.throws(
      () =>
        loadScheduledSlotConfig({
          ...scheduledSlotEnvironment,
          [name]: value,
        }),
      message,
      `${name}=${value}`,
    );
  }
});
