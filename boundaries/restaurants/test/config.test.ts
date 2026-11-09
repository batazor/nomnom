import assert from "node:assert/strict";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  CONFIG_API_VERSION,
  CONFIG_KIND,
  database,
  loadConfig,
  loadIdentityConfig,
  loadKafkaConfig,
  loadScheduledSlotConfig,
  loadSchemaRegistryConfig,
  loadSimulatorConfig,
  loadSpiceDbConfig,
  loadTelemetryConfig,
  loadValkeyConfig,
  parseConfigDocument,
  readConfigFile,
  specSchema,
} from "../pkg/config/config.ts";

/** Configuration file of `name` with `spec`, as a YAML document. */
const configYaml = (name: string, spec: string) =>
  `apiVersion: ${CONFIG_API_VERSION}
kind: ${CONFIG_KIND}
metadata:
  name: ${name}
spec:
${spec}`;

const configFile = async (content: string) => {
  const directory = await mkdtemp(join(tmpdir(), "restaurants-config-"));
  const path = join(directory, "config.yaml");
  await writeFile(path, content);
  return path;
};

test("AC-1067 a process accepts the file of its own role", async () => {
  const spec = readConfigFile(
    {
      CONFIG_FILE: await configFile(
        configYaml(
          "restaurants",
          `  shutdown:
    drainTimeout: 20s
  telemetry:
    endpoint: http://alloy:4317
  tracing:
    enabled: true
`,
        ),
      ),
    },
    "restaurants",
  );
  assert.equal(spec.shutdown.drainTimeout, 20_000);
  assert.equal(spec.tracing.enabled, true);
  assert.equal(
    parseConfigDocument(
      {
        apiVersion: CONFIG_API_VERSION,
        kind: CONFIG_KIND,
        metadata: { name: "restaurants-consumer" },
        spec: {},
      },
      "restaurants-consumer",
    ).shutdown.drainTimeout,
    20_000,
  );
});

test("the example files are valid configurations of their roles", () => {
  const example = (name: string) =>
    fileURLToPath(new URL(`../${name}`, import.meta.url));
  const secrets = {
    MONGODB__URI: "mongodb://m",
    VALKEY__URL: "redis://v",
    SPICEDB__PRESHARED_KEY: "devkey",
    POD_NAME: "local",
  };
  const api = readConfigFile(
    { ...secrets, CONFIG_FILE: example("config.example.yaml") },
    "restaurants",
  );
  loadConfig(api);
  loadTelemetryConfig(api, secrets, "restaurants");
  loadSimulatorConfig(api);
  loadScheduledSlotConfig(api);
  loadIdentityConfig(api);
  loadSpiceDbConfig(api, secrets);
  loadValkeyConfig(api, secrets);
  database.load(api, secrets, "api");
  const consumer = readConfigFile(
    { ...secrets, CONFIG_FILE: example("config.consumer.example.yaml") },
    "restaurants-consumer",
  );
  loadConfig(consumer);
  loadTelemetryConfig(consumer, secrets, "restaurants-consumer");
  loadKafkaConfig(consumer);
  loadSchemaRegistryConfig(consumer);
  loadValkeyConfig(consumer, secrets);
  assert.equal(
    database.load(consumer, secrets, "consumer").pool.maxPoolSize,
    5,
  );
  // restaurants-admin runs next to the consumer with its file.
  readConfigFile(
    { CONFIG_FILE: example("config.consumer.example.yaml") },
    "restaurants-admin",
  );
});

test("AC-1068 another apiVersion or kind stops startup and names the expected ones", () => {
  for (const header of [
    { apiVersion: "config.nomnom.dev/v1beta1", kind: CONFIG_KIND },
    { apiVersion: CONFIG_API_VERSION, kind: "Delivery" },
    { kind: CONFIG_KIND },
  ]) {
    assert.throws(
      () =>
        parseConfigDocument(
          { ...header, metadata: { name: "restaurants" }, spec: {} },
          "restaurants",
        ),
      /expected apiVersion config\.nomnom\.dev\/v1alpha1 and kind Restaurants/,
      JSON.stringify(header),
    );
  }
  assert.throws(
    () => parseConfigDocument("restaurants", "restaurants"),
    /expected apiVersion/,
  );
});

test("AC-1069 the file of another role stops startup", () => {
  assert.throws(
    () =>
      parseConfigDocument(
        {
          apiVersion: CONFIG_API_VERSION,
          kind: CONFIG_KIND,
          metadata: { name: "restaurants" },
          spec: {},
        },
        "restaurants-consumer",
      ),
    /metadata\.name: expected the role restaurants-consumer, got "restaurants"/,
  );
});

test("AC-1070 restaurants-admin accepts the file of the pod's role", () => {
  for (const name of ["restaurants-consumer", "restaurants"]) {
    const spec = parseConfigDocument(
      {
        apiVersion: CONFIG_API_VERSION,
        kind: CONFIG_KIND,
        metadata: { name },
        spec: { kafka: { brokers: "kafka:9092" } },
      },
      "restaurants-admin",
    );
    assert.deepEqual(loadKafkaConfig(spec).brokers, ["kafka:9092"]);
  }
  // The admin still checks apiVersion and kind.
  assert.throws(
    () =>
      parseConfigDocument(
        {
          apiVersion: CONFIG_API_VERSION,
          kind: "Orders",
          metadata: { name: "restaurants-consumer" },
          spec: {},
        },
        "restaurants-admin",
      ),
    /expected apiVersion/,
  );
});

test("AC-1071 a missing CONFIG_FILE or file stops startup", async () => {
  for (const environment of [{}, { CONFIG_FILE: " " }]) {
    assert.throws(
      () => readConfigFile(environment, "restaurants"),
      /CONFIG_FILE is required/,
    );
  }
  const directory = await mkdtemp(join(tmpdir(), "restaurants-config-"));
  assert.throws(
    () =>
      readConfigFile(
        { CONFIG_FILE: join(directory, "missing.yaml") },
        "restaurants",
      ),
    /missing\.yaml: cannot read the file/,
  );
  for (const content of ["", "spec: [", "---\na: 1\n---\nb: 2\n"]) {
    const path = await configFile(content);
    assert.throws(
      () => readConfigFile({ CONFIG_FILE: path }, "restaurants"),
      /invalid YAML|expected apiVersion/,
      JSON.stringify(content),
    );
  }
});

test("AC-1072 an unknown key stops startup with its path", async () => {
  const path = await configFile(
    configYaml(
      "restaurants",
      `  shutdown:
    drainTimout: 20s
  mongodb:
    pool:
      maxPoolSize: 5
`,
    ),
  );
  assert.throws(
    () => readConfigFile({ CONFIG_FILE: path }, "restaurants"),
    (error: Error) =>
      error.message.includes("spec.shutdown.drainTimout: unknown field") &&
      error.message.includes("spec.mongodb.pool: unknown field") &&
      error.message.startsWith(path),
  );
});

test("AC-1073 an unknown top-level or metadata key stops startup", () => {
  const document = {
    apiVersion: CONFIG_API_VERSION,
    kind: CONFIG_KIND,
    metadata: { name: "restaurants" },
    spec: {},
  };
  assert.throws(
    () =>
      parseConfigDocument({ ...document, status: {} }, "restaurants-consumer"),
    (error: Error) => error.message === "status: unknown field",
  );
  assert.throws(
    () =>
      parseConfigDocument(
        { ...document, metadata: { name: "restaurants", labels: {} } },
        "restaurants",
      ),
    /metadata\.labels: unknown field/,
  );
  assert.throws(
    () => parseConfigDocument({ ...document, spec: undefined }, "restaurants"),
    (error: Error) => error.message.startsWith("spec: "),
  );
});

test("sections of the other role of the kind are accepted", () => {
  const spec = parseConfigDocument(
    {
      apiVersion: CONFIG_API_VERSION,
      kind: CONFIG_KIND,
      metadata: { name: "restaurants-consumer" },
      spec: {
        simulator: { interval: "5s", minDelay: "30s", maxDelay: "2m" },
        identity: { audience: "nomnom.restaurants" },
        kafka: { brokers: "kafka:9092", consumerGroup: "restaurants" },
      },
    },
    "restaurants-consumer",
  );
  assert.equal(spec.simulator.interval, 5000);
});

test("AC-1074 env of a non-secret setting has no effect", async () => {
  const path = await configFile(
    configYaml(
      "restaurants",
      `  shutdown:
    drainTimeout: 20s
`,
    ),
  );
  const spec = readConfigFile(
    {
      CONFIG_FILE: path,
      SHUTDOWN__DRAIN_TIMEOUT: "0ms",
      SERVER__PORT: "9999",
      PORT: "9998",
      MONGODB__DATABASE: "other",
      CACHE__TTL: "1h",
    },
    "restaurants",
  );
  const config = loadConfig(spec);
  assert.equal(config.shutdown.drainTimeoutMs, 20_000);
  assert.equal(config.server.port, 8082);
  const mongodb = database.load(
    spec,
    { MONGODB__URI: "mongodb://m", MONGODB__DATABASE: "other" },
    "api",
  );
  assert.equal(mongodb.database, "restaurants");
  assert.equal(
    loadValkeyConfig(spec, { VALKEY__URL: "redis://v", CACHE__TTL: "1h" })
      .ttlMs,
    60_000,
  );
});

test("AC-1075 a secret in the file is an unknown key", () => {
  for (const [section, key] of [
    ["mongodb", "uri"],
    ["valkey", "url"],
    ["spicedb", "presharedKey"],
  ] as const) {
    assert.throws(
      () =>
        parseConfigDocument(
          {
            apiVersion: CONFIG_API_VERSION,
            kind: CONFIG_KIND,
            metadata: { name: "restaurants" },
            spec: { [section]: { [key]: "secret" } },
          },
          "restaurants",
        ),
      new RegExp(`spec\\.${section}\\.${key}: unknown field`),
    );
  }
});

test("AC-1076 an empty required secret stops startup", () => {
  const spec = specSchema.parse({ spicedb: { endpoint: "spicedb:50051" } });
  for (const uri of [undefined, "", "  "]) {
    assert.throws(
      () => database.load(spec, { MONGODB__URI: uri }, "api"),
      /MONGODB__URI is required/,
    );
  }
  assert.throws(
    () => loadValkeyConfig(spec, { VALKEY__URL: "" }),
    /VALKEY__URL is required/,
  );
});

test("an invalid value in the file stops startup with its path", () => {
  for (const [spec, message] of [
    [{ shutdown: { drainTimeout: "0ms" } }, /spec\.shutdown\.drainTimeout: /],
    [{ shutdown: { readinessDelay: "5" } }, /expected a duration/],
    [{ server: { port: "8082" } }, /spec\.server\.port: /],
    [{ server: { port: 70000 } }, /spec\.server\.port: /],
    [{ tracing: { enabled: "true" } }, /spec\.tracing\.enabled: /],
  ] as const) {
    assert.throws(
      () =>
        parseConfigDocument(
          {
            apiVersion: CONFIG_API_VERSION,
            kind: CONFIG_KIND,
            metadata: { name: "restaurants" },
            spec,
          },
          "restaurants",
        ),
      message,
      JSON.stringify(spec),
    );
  }
});

test("the process tunables default to the explicit defaults", () => {
  const config = loadConfig(specSchema.parse({}));
  assert.deepEqual(config.server, { port: 8082 });
  assert.deepEqual(config.management, { port: 9082 });
  assert.deepEqual(config.shutdown, {
    readinessDelayMs: 5000,
    drainTimeoutMs: 20_000,
    telemetryFlushTimeoutMs: 5000,
  });
});

test("reads the shutdown budget from the file", () => {
  const config = loadConfig(
    specSchema.parse({
      shutdown: {
        readinessDelay: "0s",
        drainTimeout: "1500ms",
        telemetryFlushTimeout: "2s",
      },
    }),
  );
  assert.deepEqual(config.shutdown, {
    readinessDelayMs: 0,
    drainTimeoutMs: 1500,
    telemetryFlushTimeoutMs: 2000,
  });
  assert.throws(() =>
    specSchema.parse({ shutdown: { telemetryFlushTimeout: "0s" } }),
  );
});

test("rejects a management port equal to the server port", () => {
  assert.throws(
    () => loadConfig(specSchema.parse({ management: { port: 8082 } })),
    /spec\.management\.port must differ/,
  );
});

test("MongoDB config defaults the database and timeout", () => {
  assert.deepEqual(
    database.load(
      specSchema.parse({}),
      { MONGODB__URI: "mongodb://mongo:27017/?replicaSet=rs0" },
      "api",
    ),
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
  const spec = specSchema.parse({});
  const maxPoolSize = (process: "api" | "consumer") => {
    const { pool } = database.load(
      spec,
      { MONGODB__URI: "mongodb://m" },
      process,
    );
    return [pool.maxPoolSize, pool.maxConnecting];
  };
  assert.deepEqual(maxPoolSize("api"), [20, 2]);
  assert.deepEqual(maxPoolSize("consumer"), [5, 2]);
});

test("MongoDB pool reads spec.mongodb", () => {
  const mongodb = database.load(
    specSchema.parse({
      mongodb: {
        maxPoolSize: 8,
        minPoolSize: 1,
        maxIdleTime: "1m",
        waitQueueTimeout: "750ms",
        maxConnecting: 4,
      },
    }),
    { MONGODB__URI: "mongodb://m" },
    "api",
  );
  assert.deepEqual(mongodb.pool, {
    maxPoolSize: 8,
    minPoolSize: 1,
    maxIdleTimeMs: 60_000,
    waitQueueTimeoutMs: 750,
    maxConnecting: 4,
  });
  // The default maxConnecting never exceeds a one-connection pool.
  const single = database.load(
    specSchema.parse({ mongodb: { maxPoolSize: 1 } }),
    { MONGODB__URI: "mongodb://m" },
    "api",
  );
  assert.equal(single.pool.maxConnecting, 1);
});

test("MongoDB pool rejects values out of range at startup", () => {
  const invalid: Record<string, unknown>[] = [
    { maxPoolSize: 0 },
    { maxPoolSize: 101 },
    { maxPoolSize: 2.5 },
    { maxPoolSize: "many" },
    { minPoolSize: -1 },
    { maxPoolSize: 5, minPoolSize: 6 },
    { maxIdleTime: "0s" },
    { maxIdleTime: 300000 },
    { waitQueueTimeout: "0ms" },
    { maxConnecting: 0 },
    { maxPoolSize: 3, maxConnecting: 4 },
    { timeout: "0s" },
    { database: "a/b" },
  ];
  for (const mongodb of invalid) {
    assert.throws(
      () =>
        database.load(
          specSchema.parse({ mongodb }),
          { MONGODB__URI: "mongodb://m" },
          "api",
        ),
      JSON.stringify(mongodb),
    );
  }
  assert.throws(
    () =>
      database.load(
        specSchema.parse({ mongodb: { maxPoolSize: 5, minPoolSize: 6 } }),
        { MONGODB__URI: "mongodb://m" },
        "api",
      ),
    /spec\.mongodb\.minPoolSize/,
  );
  assert.throws(() =>
    database.load(specSchema.parse({}), { MONGODB__URI: "http://m" }, "api"),
  );
});

test("durations accept hours and days", () => {
  assert.equal(
    loadConfig(specSchema.parse({ reconciler: { interval: "36h" } })).reconciler
      .intervalMs,
    36 * 3_600_000,
  );
  assert.equal(
    loadConfig(specSchema.parse({ reconciler: { grace: "1d" } })).reconciler
      .graceMs,
    86_400_000,
  );
  assert.throws(() => specSchema.parse({ reconciler: { grace: "7w" } }));
});

test("Kafka is required: a missing spec.kafka.brokers stops startup", () => {
  for (const kafka of [{}, { brokers: " " }]) {
    assert.throws(
      () => loadKafkaConfig(specSchema.parse({ kafka })),
      /spec\.kafka\.brokers is required/,
    );
  }
});

test("Kafka config parses brokers and rejects malformed ones", () => {
  assert.deepEqual(
    loadKafkaConfig(
      specSchema.parse({ kafka: { brokers: " kafka-0:9092, kafka-1:9092 " } }),
    ),
    {
      brokers: ["kafka-0:9092", "kafka-1:9092"],
      clientId: "restaurants-consumer",
    },
  );
  for (const brokers of ["kafka-0", "a:1,,b:2"]) {
    assert.throws(
      () => loadKafkaConfig(specSchema.parse({ kafka: { brokers } })),
      /spec\.kafka\.brokers: malformed broker/,
    );
  }
});

test("FR-719: the consumer requires an http(s) Schema Registry url", () => {
  assert.throws(
    () => loadSchemaRegistryConfig(specSchema.parse({})),
    /spec\.schemaRegistry\.url is required/,
  );
  assert.throws(() =>
    loadSchemaRegistryConfig(
      specSchema.parse({ schemaRegistry: { url: "kafka:8081" } }),
    ),
  );
  const config = loadSchemaRegistryConfig(
    specSchema.parse({ schemaRegistry: { url: "http://kafka:8081" } }),
  );
  assert.equal(config.url.href, "http://kafka:8081/");
  assert.equal(config.timeoutMs, 5_000);
});

test("the consumer reads its group from the oldest offset with bounded retries by default", () => {
  assert.deepEqual(loadConfig(specSchema.parse({})).consumer, {
    groupId: "restaurants",
    fromBeginning: true,
    maxRetries: 5,
    retryInitialIntervalMs: 100,
    retryMaxIntervalMs: 5000,
  });
  assert.deepEqual(
    loadConfig(
      specSchema.parse({
        kafka: { consumerGroup: "restaurants-test", initialOffset: "newest" },
        consumer: {
          maxRetries: 0,
          retryInitialInterval: "1s",
          retryMaxInterval: "1m",
        },
      }),
    ).consumer,
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
  assert.throws(() =>
    specSchema.parse({ kafka: { consumerGroup: "bad group" } }),
  );
  assert.throws(() => specSchema.parse({ kafka: { initialOffset: "latest" } }));
  assert.throws(() => specSchema.parse({ consumer: { maxRetries: -1 } }));
  assert.throws(
    () =>
      loadConfig(
        specSchema.parse({
          consumer: { retryInitialInterval: "10s", retryMaxInterval: "1s" },
        }),
      ),
    /spec\.consumer\.retryMaxInterval/,
  );
});

test("kitchen tunables default to the MVP decisions", () => {
  assert.deepEqual(loadConfig(specSchema.parse({})).kitchen, {
    preparationTimeMs: 20 * 60_000,
    repeatPolicy: "succeed-without-event",
    cancellationDuringPreparation: "reject",
    queueAgingFactor: 2,
  });
});

test("kitchen tunables are read from the file", () => {
  assert.deepEqual(
    loadConfig(
      specSchema.parse({
        kitchen: {
          preparationTime: "45m",
          repeatPolicy: "reject",
          cancellationDuringPreparation: "allow",
        },
        kitchenQueue: { agingFactor: 3.5 },
      }),
    ).kitchen,
    {
      preparationTimeMs: 45 * 60_000,
      repeatPolicy: "reject",
      cancellationDuringPreparation: "allow",
      queueAgingFactor: 3.5,
    },
  );
});

test("kitchen tunables reject malformed values", () => {
  const issue = (spec: Record<string, unknown>) =>
    parseConfigDocument(
      {
        apiVersion: CONFIG_API_VERSION,
        kind: CONFIG_KIND,
        metadata: { name: "restaurants" },
        spec,
      },
      "restaurants",
    );
  assert.throws(() => issue({ kitchen: { preparationTime: "0m" } }));
  assert.throws(() => issue({ kitchen: { preparationTime: "20" } }));
  assert.throws(
    () => issue({ kitchen: { repeatPolicy: "ignore" } }),
    /spec\.kitchen\.repeatPolicy: expected succeed-without-event or reject/,
  );
  assert.throws(
    () => issue({ kitchen: { cancellationDuringPreparation: "maybe" } }),
    /spec\.kitchen\.cancellationDuringPreparation: expected allow or reject/,
  );
  for (const agingFactor of [0, -1, "soon"]) {
    assert.throws(
      () => issue({ kitchenQueue: { agingFactor } }),
      /spec\.kitchenQueue\.agingFactor/,
    );
  }
});

test("FR-657 reads the wait time parameters and rejects inverted thresholds", () => {
  assert.deepEqual(loadConfig(specSchema.parse({})).waitTime, {
    sampleSize: 50,
    recentWindowMs: 30 * 60_000,
    elevatedFactor: 1.5,
    normalFactor: 1.2,
  });
  assert.equal(
    loadConfig(specSchema.parse({ waitTime: { recentWindow: "1h" } })).waitTime
      .recentWindowMs,
    3_600_000,
  );
  assert.throws(
    () =>
      loadConfig(
        specSchema.parse({
          waitTime: { elevatedFactor: 1.2, normalFactor: 1.2 },
        }),
      ),
    /spec\.waitTime\.normalFactor must be less/,
  );
  assert.throws(() => specSchema.parse({ waitTime: { sampleSize: 0 } }));
  assert.throws(() =>
    specSchema.parse({ waitTime: { elevatedFactor: "fast" } }),
  );
});

/** spec.scheduledSlot of config.example.yaml. */
const scheduledSlot = {
  width: "15m",
  minLead: "45m",
  maxHorizon: "48h",
  holdTtl: "10m",
  alternatives: 3,
};

test("FR-694 reads the Delivery Window grid from spec.scheduledSlot", () => {
  assert.deepEqual(
    loadScheduledSlotConfig(specSchema.parse({ scheduledSlot })),
    {
      widthMs: 15 * 60_000,
      minLeadMs: 45 * 60_000,
      maxHorizonMs: 48 * 3_600_000,
      holdTtlMs: 10 * 60_000,
      alternatives: 3,
    },
  );
});

test("FR-694 every spec.scheduledSlot value is required", () => {
  for (const name of Object.keys(scheduledSlot)) {
    assert.throws(
      () =>
        loadScheduledSlotConfig(
          specSchema.parse({
            scheduledSlot: { ...scheduledSlot, [name]: undefined },
          }),
        ),
      new RegExp(`spec\\.scheduledSlot\\.${name} is required`),
    );
  }
});

test("AC-937 a width that does not divide 30 minutes stops startup", () => {
  for (const width of ["20m", "7m", "45m", "1h"]) {
    assert.throws(
      () =>
        loadScheduledSlotConfig(
          specSchema.parse({ scheduledSlot: { ...scheduledSlot, width } }),
        ),
      /spec\.scheduledSlot\.width must divide 30 minutes/,
    );
  }
  for (const width of ["5m", "10m", "15m", "30m"]) {
    assert.equal(
      loadScheduledSlotConfig(
        specSchema.parse({ scheduledSlot: { ...scheduledSlot, width } }),
      ).widthMs % 60_000,
      0,
    );
  }
});

test("FR-694 malformed Delivery Window values stop startup", () => {
  for (const [name, value, message] of [
    ["minLead", "48h", /minLead must be shorter/],
    ["minLead", "49h", /minLead must be shorter/],
    ["holdTtl", "0m", /must be positive/],
    ["width", "15", /expected a duration/],
    ["alternatives", -1, /expected zero or more/],
    ["alternatives", "two", /expected a number/],
    ["alternatives", 1.5, /expected an integer/],
  ] as const) {
    assert.throws(
      () =>
        loadScheduledSlotConfig(
          specSchema.parse({
            scheduledSlot: { ...scheduledSlot, [name]: value },
          }),
        ),
      message,
      `${name}=${value}`,
    );
  }
});

test("telemetry defaults: no export, grpc, process name and info logs", () => {
  assert.deepEqual(
    loadTelemetryConfig(
      specSchema.parse({}),
      { POD_NAME: "restaurants-abc" },
      "restaurants",
    ),
    {
      serviceName: "restaurants",
      instanceId: "restaurants-abc",
      resourceAttributes: {},
      traces: undefined,
      metrics: undefined,
      logLevel: "INFO",
    },
  );
  assert.deepEqual(
    loadTelemetryConfig(
      specSchema.parse({
        telemetry: {
          endpoint: "http://alloy:4318",
          protocol: "http/protobuf",
          serviceName: "restaurants-blue",
          resourceAttributes: { "deployment.environment.name": "kind" },
        },
        tracing: { enabled: true },
        metrics: { enabled: true },
        logging: { level: "debug" },
      }),
      { POD_NAME: "restaurants-abc" },
      "restaurants",
    ),
    {
      serviceName: "restaurants-blue",
      instanceId: "restaurants-abc",
      resourceAttributes: { "deployment.environment.name": "kind" },
      traces: { endpoint: "http://alloy:4318", protocol: "http/protobuf" },
      metrics: {
        endpoint: "http://alloy:4318",
        protocol: "http/protobuf",
        exportIntervalMs: 60_000,
      },
      logLevel: "DEBUG",
    },
  );
});

test("AC-1077 export enabled without an endpoint stops startup", () => {
  for (const spec of [
    { tracing: { enabled: true } },
    { metrics: { enabled: true } },
    { tracing: { enabled: true }, telemetry: { endpoint: "" } },
  ]) {
    assert.throws(
      () =>
        loadTelemetryConfig(
          specSchema.parse(spec),
          { POD_NAME: "restaurants-abc" },
          "restaurants",
        ),
      /spec\.telemetry\.endpoint is required/,
      JSON.stringify(spec),
    );
  }
  assert.throws(
    () =>
      loadTelemetryConfig(
        specSchema.parse({
          telemetry: { endpoint: "alloy:4317" },
          tracing: { enabled: true },
        }),
        { POD_NAME: "restaurants-abc" },
        "restaurants",
      ),
    /must be an http\(s\) url/,
  );
});

test("AC-1078 an unknown OTLP protocol stops startup and lists both", () => {
  assert.throws(
    () =>
      parseConfigDocument(
        {
          apiVersion: CONFIG_API_VERSION,
          kind: CONFIG_KIND,
          metadata: { name: "restaurants" },
          spec: { telemetry: { protocol: "http/json" } },
        },
        "restaurants",
      ),
    /spec\.telemetry\.protocol: expected grpc or http\/protobuf/,
  );
});

test("AC-1080 an endpoint without the flags exports nothing", () => {
  const config = loadTelemetryConfig(
    specSchema.parse({ telemetry: { endpoint: "http://alloy:4317" } }),
    { POD_NAME: "restaurants-abc" },
    "restaurants",
  );
  assert.equal(config.traces, undefined);
  assert.equal(config.metrics, undefined);
});

test("AC-1081 resource attributes must not override the process identity", () => {
  for (const key of ["service.name", "service.instance.id"]) {
    assert.throws(
      () =>
        parseConfigDocument(
          {
            apiVersion: CONFIG_API_VERSION,
            kind: CONFIG_KIND,
            metadata: { name: "restaurants" },
            spec: { telemetry: { resourceAttributes: { [key]: "other" } } },
          },
          "restaurants",
        ),
      new RegExp(
        `spec\\.telemetry\\.resourceAttributes\\.${key.replaceAll(".", "\\.")}: must not be set`,
      ),
    );
  }
});

test("AC-1082 a missing POD_NAME stops startup", () => {
  for (const environment of [{}, { POD_NAME: "" }]) {
    assert.throws(
      () =>
        loadTelemetryConfig(specSchema.parse({}), environment, "restaurants"),
      /POD_NAME is required/,
    );
  }
});

test("FR-765 OTEL_* and LOG__LEVEL do not configure telemetry", () => {
  const config = loadTelemetryConfig(
    specSchema.parse({ logging: { level: "warn" } }),
    {
      POD_NAME: "restaurants-abc",
      OTEL_SERVICE_NAME: "other",
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://other:4317",
      OTEL_RESOURCE_ATTRIBUTES: "service.version=9.9.9",
      LOG__LEVEL: "debug",
    },
    "restaurants",
  );
  assert.equal(config.serviceName, "restaurants");
  assert.equal(config.traces, undefined);
  assert.equal(config.metrics, undefined);
  assert.deepEqual(config.resourceAttributes, {});
  assert.equal(config.logLevel, "WARN");
});
