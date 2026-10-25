// Command restaurants-relay publishes the Transactional Outbox of restaurants
// to Kafka (ADR 13, ADR 31): a polling relay with a single active instance by
// lease in `outbox_leases`; other replicas wait as failover. It follows the
// same runtime contract as cmd/restaurants (pkg/lifecycle): management port
// with /health/*, SIGTERM, readiness delay and drain.
import { hostname } from "node:os";

import { loadConfig, loadLocalEnv } from "../../pkg/config/config.ts";
import { KafkaPublisher } from "../../pkg/infrastructure/kafka/publisher.ts";
import { connectMongoDb } from "../../pkg/infrastructure/mongodb/client.ts";
import { MongoOutboxRelayStore } from "../../pkg/infrastructure/mongodb/outbox-relay-store.ts";
import { OtelRelayMetrics } from "../../pkg/infrastructure/outbox/metrics.ts";
import { OutboxRelay } from "../../pkg/infrastructure/outbox/relay.ts";
import { IdleWorker, Lifecycle } from "../../pkg/lifecycle/lifecycle.ts";
import { PeriodicLoop, WorkerWithLoops } from "../../pkg/lifecycle/periodic.ts";
import { startTelemetry } from "../../pkg/telemetry/telemetry.ts";

const log = (message: string) => console.log(`restaurants-relay ${message}`);

loadLocalEnv();
const config = loadConfig();
if (!config.mongodb.enabled) {
  throw new Error(
    "MONGODB__URI is required: restaurants-relay reads the Outbox from MongoDB",
  );
}
if (!config.kafka.enabled) {
  throw new Error(
    "KAFKA__BROKERS is required: restaurants-relay publishes the Outbox to Kafka",
  );
}

const telemetry = startTelemetry("restaurants-relay");
const mongo = await connectMongoDb(config.mongodb);
let publisher: KafkaPublisher | undefined;
try {
  publisher = await KafkaPublisher.connect(config.kafka);
  const relay = new OutboxRelay({
    store: new MongoOutboxRelayStore(mongo.db),
    publisher,
    owner: `${hostname()}/${process.pid}`,
    batchSize: config.outbox.batchSize,
    leaseTtlMs: config.outbox.leaseTtlMs,
    retentionMs: config.outbox.retentionMs,
    metrics: new OtelRelayMetrics("restaurants"),
    log,
  });
  await new Lifecycle({
    managementPort: config.management.port,
    readinessDelayMs: config.shutdown.readinessDelayMs,
    drainTimeoutMs: config.shutdown.drainTimeoutMs,
  }).run(
    new WorkerWithLoops(
      new IdleWorker(async () => {
        await relay.release();
      }),
      [
        new PeriodicLoop(
          async (signal) => {
            await relay.cycle(signal);
          },
          {
            initialDelayMs: 0,
            intervalMs: config.outbox.pollIntervalMs,
            log: (message) => console.error(`restaurants-relay ${message}`),
          },
        ),
      ],
    ),
    { log },
  );
} catch (error) {
  console.error("restaurants-relay stopped with error", error);
  process.exitCode = 1;
} finally {
  await publisher?.close();
  await mongo.close();
  await telemetry.shutdown();
}
