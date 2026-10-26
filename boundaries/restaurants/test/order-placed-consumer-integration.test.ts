import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";
import { setTimeout as delay } from "node:timers/promises";
import { create, toBinary } from "@bufbuild/protobuf";
import kafka from "@confluentinc/kafka-javascript";
import { createConsumerContainer } from "../di/consumer.ts";
import { OrderPlacedSchema } from "../infrastructure/grpc/gen/nomnom/orders/v1/events_pb.js";
import { migrateRestaurantsDatabase } from "../infrastructure/repository/migrate.ts";
import { loadConfig } from "../pkg/config/config.ts";
import {
  createConsumerClient,
  KafkaConsumerWorker,
} from "../pkg/infrastructure/kafka/consumer.ts";
import { KafkaPublisher } from "../pkg/infrastructure/kafka/publisher.ts";
import {
  connectDatabase,
  type DatabaseConnection,
} from "../pkg/infrastructure/repository/client.ts";
import {
  INBOX_COLLECTION,
  type InboxDocument,
} from "../pkg/infrastructure/repository/inbox.ts";
import { silentLogger } from "../pkg/telemetry/logger.ts";
import { Restaurant } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import { RestaurantOrderState } from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import { ORDER_PLACED_EVENT_TYPE } from "../slices/restaurantorder/infrastructure/integration/order-placed.ts";
import { databaseUri } from "./support/database.ts";
import { kafkaBrokers } from "./support/global-containers.ts";
import { offlineValkey, testValkeyConfig } from "./support/valkey.ts";

// OrderPlaced → Kafka → restaurants-consumer → Inbox + Restaurant Order in
// MongoDB against real infrastructure: MongoDB and Kafka are the testcontainers
// of the global setup (test/support/global-containers.ts).
const uri = databaseUri();
const brokers = kafkaBrokers();

describe("OrderPlaced consumer with MongoDB and Kafka", () => {
  // A topic per run keeps other runs' events out of this consumer group.
  const topic = `orders.v1.order-events.it-${randomUUID().slice(0, 8)}`;
  const kafkaConfig = {
    enabled: true as const,
    brokers: brokers,
    clientId: "restaurants-consumer-test",
  };
  const client = new kafka.KafkaJS.Kafka({
    kafkaJS: {
      brokers: kafkaConfig.brokers,
      logLevel: kafka.KafkaJS.logLevel.ERROR,
    },
  });
  let database: DatabaseConnection;
  let publisher: KafkaPublisher;

  before(async () => {
    database = await connectDatabase({
      enabled: true,
      uri,
      database: `restaurants_consumer_${randomUUID().slice(0, 8)}`,
      timeoutMs: 5000,
    });
    await migrateRestaurantsDatabase(database.db);
    const admin = client.admin();
    await admin.connect();
    await admin.createTopics({
      topics: [
        { topic, numPartitions: 3 },
        { topic: `${topic}.dlq`, numPartitions: 1 },
      ],
    });
    await admin.disconnect();
    publisher = await KafkaPublisher.connect(kafkaConfig);
  });

  after(async () => {
    await publisher?.close();
    const admin = client.admin();
    await admin.connect();
    await admin
      .deleteTopics({ topics: [topic, `${topic}.dlq`] })
      .catch(() => {});
    await admin.disconnect();
    if (database === undefined) return;
    await database.db.dropDatabase();
    await database.close();
  });

  const publishOrderPlaced = (
    eventId: string,
    orderId: string,
    restaurantId: string,
    items: { menuItemId: string; quantity: number }[],
  ) =>
    publisher.publish({
      topic,
      key: orderId,
      value: toBinary(
        OrderPlacedSchema,
        create(OrderPlacedSchema, {
          orderId,
          customerId: "customer-1",
          restaurantId,
          marketId: "berlin",
          total: { amount: "19.00", currency: "EUR" },
          items: items.map((item) => ({
            ...item,
            unitPrice: { amount: "9.50", currency: "EUR" },
          })),
        }),
      ),
      headers: {
        event_id: eventId,
        event_type: ORDER_PLACED_EVENT_TYPE,
        occurred_at: new Date().toISOString(),
        aggregate_type: "order",
        aggregate_id: orderId,
        aggregate_version: "1",
      },
    });

  test("creates the Restaurant Order once per event_id and dead-letters poison", async () => {
    const restaurantId = `r-${randomUUID()}`;
    const orderId = `order-${randomUUID()}`;
    const poisonOrderId = `order-${randomUUID()}`;
    const groupId = `restaurants-it-${randomUUID()}`;
    const container = createConsumerContainer({
      consumer: { ...loadConfig({}).consumer, groupId },
      database,
      kafkaConsumer: createConsumerClient({
        brokers: kafkaConfig.brokers,
        clientId: kafkaConfig.clientId,
        groupId,
        fromBeginning: true,
      }),
      // Only the order-events group runs here; cache refresh has its own test.
      cacheRefreshConsumer: {
        connect: async () => {},
        subscribe: async () => {},
        run: async () => {},
        disconnect: async () => {},
      },
      valkeyConfig: testValkeyConfig("redis://127.0.0.1:1"),
      valkey: offlineValkey(),
      deadLetters: publisher,
      logger: silentLogger,
    });
    await container.cradle.restaurants.save(
      Restaurant.register({
        id: restaurantId,
        name: "Pho House",
        ownerId: "owner",
        marketId: "berlin",
        registeredAt: new Date(),
      }),
    );
    const items = [{ menuItemId: "pho", quantity: 2 }];
    const eventId = randomUUID();
    await publishOrderPlaced(eventId, orderId, restaurantId, items);
    // Redelivery of the same event (CDC replay after a restart) is a duplicate.
    await publishOrderPlaced(eventId, orderId, restaurantId, items);
    await publishOrderPlaced(randomUUID(), poisonOrderId, restaurantId, []);

    const worker = new KafkaConsumerWorker({
      client: container.cradle.kafkaConsumer,
      topics: [topic],
      handler: container.cradle.messageProcessor,
    });
    const deadLetters = consumeOne(client, `${topic}.dlq`);
    await worker.start();
    const running = worker.run();
    try {
      const orders = database.db.collection("restaurant_orders");
      const inbox = database.db.collection<InboxDocument>(INBOX_COLLECTION);
      const deadline = Date.now() + 30_000;
      while (
        (await inbox.countDocuments({ eventId })) === 0 &&
        Date.now() < deadline
      ) {
        await delay(200);
      }
      const dead = await deadLetters;
      // Give the duplicate a chance to be processed before counting.
      await delay(500);

      const stored = await orders.find({ orderId }).toArray();
      assert.equal(stored.length, 1);
      assert.equal(stored[0]?.state, RestaurantOrderState.Received);
      assert.equal(stored[0]?.restaurantId, restaurantId);
      assert.equal(stored[0]?.marketId, "berlin");
      assert.deepEqual(
        await inbox
          .find({ eventId }, { projection: { _id: 0, consumer: 1 } })
          .toArray(),
        [{ consumer: "restaurants.order-placed" }],
      );
      assert.equal(await orders.countDocuments({ orderId: poisonOrderId }), 0);
      assert.equal(dead.headers.aggregate_id, poisonOrderId);
      assert.equal(dead.headers.topic_poisoned, topic);
      assert.match(dead.headers.reason_poisoned ?? "", /no items/);
    } finally {
      await worker.shutdown(AbortSignal.timeout(10_000));
      await running;
      await container.dispose();
    }
  });
});

async function consumeOne(
  client: InstanceType<typeof kafka.KafkaJS.Kafka>,
  topic: string,
): Promise<{ headers: Record<string, string> }> {
  const consumer = client.consumer({
    kafkaJS: { groupId: `dlq-test-${randomUUID()}`, fromBeginning: true },
  });
  await consumer.connect();
  await consumer.subscribe({ topics: [topic] });
  try {
    return await new Promise((resolve, reject) => {
      const timer = setTimeout(
        () => reject(new Error(`no message on ${topic}`)),
        30_000,
      );
      consumer
        .run({
          eachMessage: async ({ message }) => {
            const headers: Record<string, string> = {};
            for (const [name, value] of Object.entries(message.headers ?? {})) {
              headers[name] = String(Array.isArray(value) ? value[0] : value);
            }
            clearTimeout(timer);
            resolve({ headers });
          },
        })
        .catch(reject);
    });
  } finally {
    await consumer.disconnect();
  }
}
