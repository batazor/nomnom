// Command restaurants-consumer consumes Integration Events of other contexts
// through a Transactional Inbox in MongoDB (ADR 11, ADR 28, ADR 31): now
// `nomnom.orders.v1.OrderPlaced` of `orders.v1.order-events`, which creates
// the Restaurant Order by Receive Order Request. Replicas share one consumer
// group. It follows the same runtime contract as cmd/restaurants
// (pkg/lifecycle): management port with /health/*, SIGTERM, readiness delay
// and drain of the message in progress. The object graph is composed by
// Awilix in di/consumer.ts (ADR 5); this composition root owns the
// connections and the shutdown order.
import type { AwilixContainer } from "awilix";

import {
  type ConsumerCradle,
  createConsumerContainer,
} from "../../di/consumer.ts";
import { loadConfig, loadLocalEnv } from "../../pkg/config/config.ts";
import {
  type ConsumerClient,
  createConsumerClient,
} from "../../pkg/infrastructure/kafka/consumer.ts";
import { KafkaPublisher } from "../../pkg/infrastructure/kafka/publisher.ts";
import { connectMongoDb } from "../../pkg/infrastructure/mongodb/client.ts";
import { Lifecycle } from "../../pkg/lifecycle/lifecycle.ts";
import { startTelemetry } from "../../pkg/telemetry/telemetry.ts";

const log = (message: string) => console.log(`restaurants-consumer ${message}`);

loadLocalEnv();
const config = loadConfig(process.env, { process: "consumer" });
// MongoDB and Kafka are required: without them the process does not start.
// Collections and indexes, the Inbox included, are created by
// restaurants-migrate before the workload starts.
if (!config.mongodb.enabled) {
  throw new Error(
    "MONGODB__URI is required: restaurants-consumer writes the Inbox and Restaurant Orders to MongoDB",
  );
}
if (!config.kafka.enabled) {
  throw new Error(
    "KAFKA__BROKERS is required: restaurants-consumer reads Integration Events from Kafka",
  );
}

const telemetry = startTelemetry("restaurants-consumer");
const mongo = await connectMongoDb(config.mongodb);
let deadLetters: KafkaPublisher | undefined;
let kafkaConsumer: ConsumerClient | undefined;
let container: AwilixContainer<ConsumerCradle> | undefined;
try {
  deadLetters = await KafkaPublisher.connect(config.kafka, {
    allowAutoTopicCreation: true,
  });
  kafkaConsumer = createConsumerClient({
    brokers: config.kafka.brokers,
    clientId: config.kafka.clientId,
    groupId: config.consumer.groupId,
    fromBeginning: config.consumer.fromBeginning,
  });
  container = createConsumerContainer({
    consumer: config.consumer,
    mongo,
    kafkaConsumer,
    deadLetters,
    log,
  });
  await new Lifecycle({
    managementPort: config.management.port,
    readinessDelayMs: config.shutdown.readinessDelayMs,
    drainTimeoutMs: config.shutdown.drainTimeoutMs,
  }).run(container.cradle.worker, { log });
} catch (error) {
  console.error("restaurants-consumer stopped with error", error);
  process.exitCode = 1;
} finally {
  // The worker has disconnected the consumer (and committed offsets) on a
  // regular shutdown; a repeated disconnect is a no-op.
  await kafkaConsumer?.disconnect();
  await container?.dispose();
  await deadLetters?.close();
  await mongo.close();
  await telemetry.shutdown();
}
