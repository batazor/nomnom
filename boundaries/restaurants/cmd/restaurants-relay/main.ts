// Command restaurants-relay publishes the Transactional Outbox of restaurants
// to Kafka (ADR 13, ADR 31): a polling relay with a single active instance by
// lease in `outbox_leases`; other replicas wait as failover. It follows the
// same runtime contract as cmd/restaurants (pkg/lifecycle): management port
// with /health/*, SIGTERM, readiness delay and drain. The object graph is
// composed by Awilix in di/relay.ts (ADR 5); this composition root owns the
// connections and the shutdown order.
import type { AwilixContainer } from "awilix";

import { createRelayContainer, type RelayCradle } from "../../di/relay.ts";
import { loadConfig, loadLocalEnv } from "../../pkg/config/config.ts";
import { KafkaPublisher } from "../../pkg/infrastructure/kafka/publisher.ts";
import { connectMongoDb } from "../../pkg/infrastructure/mongodb/client.ts";
import { relayMessageTimeoutMs } from "../../pkg/infrastructure/outbox/relay.ts";
import { Lifecycle } from "../../pkg/lifecycle/lifecycle.ts";
import { startTelemetry } from "../../pkg/telemetry/telemetry.ts";

const log = (message: string) => console.log(`restaurants-relay ${message}`);

loadLocalEnv();
const config = loadConfig(process.env, { process: "relay" });
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
let container: AwilixContainer<RelayCradle> | undefined;
try {
  publisher = await KafkaPublisher.connect(config.kafka, {
    // Streams are created on first publish, as by the payments and delivery
    // relays: a fresh cluster has no restaurants.v1.* topics until then.
    allowAutoTopicCreation: true,
    messageTimeoutMs: relayMessageTimeoutMs(
      config.outbox.leaseTtlMs,
      config.shutdown.drainTimeoutMs,
    ),
  });
  container = createRelayContainer({
    outbox: config.outbox,
    db: mongo.db,
    publisher,
    log,
  });
  await new Lifecycle({
    managementPort: config.management.port,
    readinessDelayMs: config.shutdown.readinessDelayMs,
    drainTimeoutMs: config.shutdown.drainTimeoutMs,
  }).run(container.cradle.worker, { log });
} catch (error) {
  console.error("restaurants-relay stopped with error", error);
  process.exitCode = 1;
} finally {
  await container?.dispose();
  await publisher?.close();
  await mongo.close();
  await telemetry.shutdown();
}
