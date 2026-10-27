// Command restaurants-consumer consumes Integration Events of other contexts
// through a Transactional Inbox in MongoDB (ADR 11, ADR 28, ADR 31): now
// `nomnom.delivery.v1.DeliveryPickedUp` of `delivery.v1.delivery-events`, which
// records the Pickup of the Restaurant Order, and refreshes the catalog read
// cache by own events. The Restaurant Order itself is created only by the
// fulfillment workflow of orders (RPC ReceiveOrderRequest) after payment.
// Replicas share one consumer group. It follows the same runtime contract as cmd/restaurants
// (pkg/lifecycle): management port with /health/*, SIGTERM, readiness delay
// and drain of the message in progress. The object graph is composed by
// Awilix in di/consumer.ts (ADR 5); this composition root owns the
// connections and the shutdown order.
import type { AwilixContainer } from "awilix";

import {
  type ConsumerCradle,
  createConsumerContainer,
} from "../../di/consumer.ts";
import { migrateRestaurantsDatabase } from "../../infrastructure/repository/migrate.ts";
import { loadConfig, loadLocalEnv } from "../../pkg/config/config.ts";
import {
  type ConsumerClient,
  createConsumerClient,
} from "../../pkg/infrastructure/kafka/consumer.ts";
import { KafkaPublisher } from "../../pkg/infrastructure/kafka/publisher.ts";
import { connectDatabase } from "../../pkg/infrastructure/repository/client.ts";
import { connectValkey } from "../../pkg/infrastructure/valkey/client.ts";
import { Lifecycle } from "../../pkg/lifecycle/lifecycle.ts";
import { createLogger, logUncaughtErrors } from "../../pkg/telemetry/logger.ts";
import { telemetryResource } from "../../pkg/telemetry/resource.ts";
import { startTelemetry } from "../../pkg/telemetry/telemetry.ts";

const SERVICE_NAME = "restaurants-consumer";

loadLocalEnv();
const logger = createLogger({ resource: telemetryResource(SERVICE_NAME) });
logUncaughtErrors(logger);
const config = loadConfig(process.env, { process: "consumer" });
// MongoDB and Kafka are required: without them the process does not start.
// Collections and indexes, the Inbox included, are migrated by the process
// itself before it consumes (local ADR 2).
if (!config.database.enabled) {
  throw new Error(
    "MONGODB__URI is required: restaurants-consumer writes the Inbox and Restaurant Orders to MongoDB",
  );
}
if (!config.kafka.enabled) {
  throw new Error(
    "KAFKA__BROKERS is required: restaurants-consumer reads Integration Events from Kafka",
  );
}
// Valkey (ADR 43) is required: the consumer refreshes the catalog read cache
// by restaurant-events and menu-events. While Valkey is down the refresh
// waits and its offsets stay uncommitted.
if (!config.valkey.enabled) {
  throw new Error(
    "VALKEY__URL is required: restaurants-consumer refreshes the catalog read cache in Valkey",
  );
}
const valkeyConfig = config.valkey;

const telemetry = startTelemetry(SERVICE_NAME);
const database = await connectDatabase(config.database);
const valkey = connectValkey(valkeyConfig, {
  connectionName: SERVICE_NAME,
  logger: logger.child({ operation: "valkey" }),
});
let deadLetters: KafkaPublisher | undefined;
let kafkaConsumer: ConsumerClient | undefined;
let cacheRefreshConsumer: ConsumerClient | undefined;
let container: AwilixContainer<ConsumerCradle> | undefined;
try {
  // Nothing to change on an already current database; a failed migration
  // stops the process before it consumes.
  await migrateRestaurantsDatabase(
    database.db,
    logger.child({ operation: "migrate" }).info,
  );
  deadLetters = await KafkaPublisher.connect(config.kafka, {
    allowAutoTopicCreation: true,
  });
  kafkaConsumer = createConsumerClient({
    brokers: config.kafka.brokers,
    clientId: config.kafka.clientId,
    groupId: config.consumer.groupId,
    fromBeginning: config.consumer.fromBeginning,
  });
  // A new refresh group starts at the end of the streams: older changes are
  // already in MongoDB, a miss reads them.
  cacheRefreshConsumer = createConsumerClient({
    brokers: config.kafka.brokers,
    clientId: config.kafka.clientId,
    groupId: valkeyConfig.refreshGroup,
    fromBeginning: false,
  });
  container = createConsumerContainer({
    consumer: config.consumer,
    database,
    kafkaConsumer,
    cacheRefreshConsumer,
    valkeyConfig,
    valkey: valkey.client,
    deadLetters,
    logger,
  });
  await new Lifecycle({
    managementPort: config.management.port,
    readinessDelayMs: config.shutdown.readinessDelayMs,
    drainTimeoutMs: config.shutdown.drainTimeoutMs,
  }).run(container.cradle.worker, {
    log: logger.child({ operation: "lifecycle" }).info,
  });
} catch (error) {
  logger.error("stopped with error", { operation: "lifecycle", error });
  process.exitCode = 1;
} finally {
  // The worker has disconnected the consumer (and committed offsets) on a
  // regular shutdown; a repeated disconnect is a no-op.
  await kafkaConsumer?.disconnect();
  await cacheRefreshConsumer?.disconnect();
  await container?.dispose();
  await valkey.close();
  await deadLetters?.close();
  await database.close();
  await telemetry.shutdown(config.shutdown.telemetryFlushTimeoutMs);
}
