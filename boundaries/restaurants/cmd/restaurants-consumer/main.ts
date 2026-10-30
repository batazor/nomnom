// Command restaurants-consumer consumes Integration Events of other contexts
// through a Transactional Inbox in MongoDB (ADR 11, ADR 28, ADR 31): now its own
// `restaurants.v1.restaurant-order-events` for Wait Time Statistics, and refreshes
// the catalog read cache by own events. The kitchen does not read neighbours: the
// Pickup comes from the fulfillment workflow of orders (RPC RecordRestaurantOrderPickup). The Restaurant Order itself is created only by the
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
import {
  type ConsumerClient,
  createConsumerClient,
} from "../../infrastructure/kafka/consumer.ts";
import { KafkaPublisher } from "../../infrastructure/kafka/publisher.ts";
import { connectDatabase } from "../../infrastructure/repository/client.ts";
import { migrateRestaurantsDatabase } from "../../infrastructure/repository/migrate.ts";
import { connectValkey } from "../../infrastructure/valkey/client.ts";
import {
  database as databaseConfig,
  loadConfig,
  loadKafkaConfig,
  loadLocalEnv,
  loadSchemaRegistryConfig,
  loadValkeyConfig,
} from "../../pkg/config/config.ts";
import { Lifecycle } from "../../pkg/lifecycle/lifecycle.ts";
import { createLogger, logUncaughtErrors } from "../../pkg/telemetry/logger.ts";
import { telemetryResource } from "../../pkg/telemetry/resource.ts";
import { startTelemetry } from "../../pkg/telemetry/telemetry.ts";

const SERVICE_NAME = "restaurants-consumer";

loadLocalEnv();
const logger = createLogger({ resource: telemetryResource(SERVICE_NAME) });
logUncaughtErrors(logger);
const config = loadConfig(process.env);
// Required infrastructure: an empty or invalid value stops startup. MongoDB
// holds the Inbox and Restaurant Orders; collections and indexes, the Inbox
// included, are migrated by the process itself before it consumes (local
// ADR 2). Kafka carries the Integration Events (ADR 28). Valkey (ADR 43) is
// the catalog read cache the consumer refreshes; while it is down the refresh
// waits and its offsets stay uncommitted. The Schema Registry resolves the
// schema of every record (FR-715).
const mongodb = databaseConfig.load(process.env, "consumer");
const kafka = loadKafkaConfig(process.env);
const valkeyConfig = loadValkeyConfig(process.env);
const schemaRegistry = loadSchemaRegistryConfig(process.env);

const telemetry = startTelemetry(SERVICE_NAME);
const database = await connectDatabase(mongodb);
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
  deadLetters = await KafkaPublisher.connect(kafka, {
    allowAutoTopicCreation: true,
  });
  kafkaConsumer = createConsumerClient({
    brokers: kafka.brokers,
    clientId: kafka.clientId,
    groupId: config.consumer.groupId,
    fromBeginning: config.consumer.fromBeginning,
  });
  // A new refresh group starts at the end of the streams: older changes are
  // already in MongoDB, a miss reads them.
  cacheRefreshConsumer = createConsumerClient({
    brokers: kafka.brokers,
    clientId: kafka.clientId,
    groupId: valkeyConfig.refreshGroup,
    fromBeginning: false,
  });
  container = createConsumerContainer({
    consumer: config.consumer,
    kitchen: config.kitchen,
    waitTime: config.waitTime,
    database,
    kafkaConsumer,
    cacheRefreshConsumer,
    valkeyConfig,
    valkey: valkey.client,
    deadLetters,
    schemaRegistry,
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
