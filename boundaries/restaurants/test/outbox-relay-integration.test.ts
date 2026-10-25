import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";

import { fromBinary } from "@bufbuild/protobuf";
import kafka from "@confluentinc/kafka-javascript";

import { PreparationFailedSchema } from "../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import { migrateRestaurantsDatabase } from "../infrastructure/mongodb/collections.ts";
import { KafkaPublisher } from "../pkg/infrastructure/kafka/publisher.ts";
import {
  connectMongoDb,
  type MongoDbConnection,
} from "../pkg/infrastructure/mongodb/client.ts";
import {
  MongoOutbox,
  OUTBOX_COLLECTION,
  type OutboxDocument,
  outboxEventId,
} from "../pkg/infrastructure/mongodb/outbox.ts";
import { MongoOutboxRelayStore } from "../pkg/infrastructure/mongodb/outbox-relay-store.ts";
import { MongoUnitOfWork } from "../pkg/infrastructure/mongodb/unit-of-work.ts";
import { OutboxRelay } from "../pkg/infrastructure/outbox/relay.ts";
import { RestaurantOrderState } from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import {
  RESTAURANT_ORDER_EVENTS_TOPIC,
  restaurantOrderEventMapper,
} from "../slices/restaurantorder/infrastructure/integration/restaurant-order-events.ts";

// Outbox → restaurants-relay → Kafka against real infrastructure, for example
//   docker run -d -p 27017:27017 mongo:8 --replSet rs0   (+ rs.initiate())
//   docker run -d -p 9092:9092 apache/kafka:4.1.0
// MONGODB_URI=mongodb://localhost:27017/?replicaSet=rs0&directConnection=true
// KAFKA_BROKERS=localhost:9092
const uri = process.env.MONGODB_URI?.trim() ?? "";
const brokers = process.env.KAFKA_BROKERS?.trim() ?? "";
const skip =
  uri === "" || brokers === ""
    ? "MONGODB_URI or KAFKA_BROKERS is not set"
    : false;

type Received = {
  key: string;
  value: Buffer;
  headers: Record<string, string>;
};

describe("Outbox relay with MongoDB and Kafka", { skip }, () => {
  let mongo: MongoDbConnection;
  let publisher: KafkaPublisher;
  const client = new kafka.KafkaJS.Kafka({
    kafkaJS: {
      brokers: brokers.split(","),
      logLevel: kafka.KafkaJS.logLevel.ERROR,
    },
  });

  before(async () => {
    mongo = await connectMongoDb({
      enabled: true,
      uri,
      database: `restaurants_relay_${randomUUID().slice(0, 8)}`,
      timeoutMs: 5000,
    });
    await migrateRestaurantsDatabase(mongo.db);
    const admin = client.admin();
    await admin.connect();
    const topics = await admin.listTopics();
    if (!topics.includes(RESTAURANT_ORDER_EVENTS_TOPIC)) {
      await admin.createTopics({
        topics: [{ topic: RESTAURANT_ORDER_EVENTS_TOPIC, numPartitions: 3 }],
      });
    }
    await admin.disconnect();
    publisher = await KafkaPublisher.connect({
      enabled: true,
      brokers: brokers.split(","),
      clientId: "restaurants-relay-test",
    });
  });

  after(async () => {
    await publisher?.close();
    if (mongo === undefined) return;
    await mongo.db.dropDatabase();
    await mongo.close();
  });

  test("publishes Outbox records with the Event Envelope headers", async () => {
    const unitOfWork = new MongoUnitOfWork(mongo.client);
    const traceparent =
      "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01";
    const outbox = new MongoOutbox(mongo.db, unitOfWork, {
      mapper: restaurantOrderEventMapper,
      metadata: () => ({ traceparent }),
    });
    const restaurantOrderId = `ro-${randomUUID()}`;
    const occurredAt = new Date("2026-10-25T09:00:00.120Z");
    const base = {
      restaurantOrderId,
      orderId: "order-42",
      restaurantId: "rest-1",
      occurredAt,
    };
    const aggregate = { type: "RestaurantOrder", id: restaurantOrderId };
    await unitOfWork.run(() =>
      outbox.add({ ...aggregate, version: 2 }, [
        { ...base, type: "RestaurantOrderQueued", queuedAt: occurredAt },
        {
          ...base,
          type: "PreparationStarted",
          preparationStartedAt: occurredAt,
        },
      ] as never),
    );
    await unitOfWork.run(() =>
      outbox.add({ ...aggregate, version: 3 }, [
        {
          ...base,
          type: "RestaurantOrderFailed",
          stage: RestaurantOrderState.Preparing,
          reason: "no gas",
        },
      ] as never),
    );
    const collection = mongo.db.collection<OutboxDocument>(OUTBOX_COLLECTION);
    assert.equal(await collection.countDocuments(), 2, "Queued is not mapped");

    const store = new MongoOutboxRelayStore(mongo.db);
    const relay = new OutboxRelay({
      store,
      publisher,
      owner: "relay-a",
      batchSize: 10,
      leaseTtlMs: 15_000,
      retentionMs: 7 * 86_400_000,
    });
    const standby = new OutboxRelay({
      store,
      publisher,
      owner: "relay-b",
      batchSize: 10,
      leaseTtlMs: 15_000,
      retentionMs: 7 * 86_400_000,
    });

    const result = await relay.cycle();
    assert.deepEqual(result, { leased: true, published: 2, deleted: 0 });
    assert.equal((await standby.cycle()).leased, false, "single active relay");
    assert.equal(await collection.countDocuments({ publishedAt: null }), 0);
    assert.deepEqual(await store.pendingStats(), []);

    const expectedIds = [
      outboxEventId(
        { ...aggregate, version: 2 },
        "nomnom.restaurants.v1.PreparationStarted",
      ),
      outboxEventId(
        { ...aggregate, version: 3 },
        "nomnom.restaurants.v1.PreparationFailed",
      ),
    ];
    const received = await consume(
      client,
      (message) => expectedIds.includes(message.headers.event_id ?? ""),
      2,
    );

    assert.deepEqual(
      received.map((message) => message.headers.event_id),
      expectedIds,
      "same partition keeps aggregate order",
    );
    const failed = received[1];
    assert.ok(failed);
    assert.equal(failed.key, restaurantOrderId);
    assert.deepEqual(failed.headers, {
      _watermill_message_uuid: expectedIds[1],
      event_id: expectedIds[1],
      event_type: "nomnom.restaurants.v1.PreparationFailed",
      occurred_at: "2026-10-25T09:00:00.12Z",
      aggregate_type: "RestaurantOrder",
      aggregate_id: restaurantOrderId,
      aggregate_version: "3",
      traceparent,
    });
    const payload = fromBinary(PreparationFailedSchema, failed.value);
    assert.equal(payload.orderId, "order-42");
    assert.equal(payload.reason, "no gas");

    await relay.release();
    assert.equal(
      (await standby.cycle()).leased,
      true,
      "failover after release",
    );
  });

  test("a failed publish stops the batch and records attempts", async () => {
    const unitOfWork = new MongoUnitOfWork(mongo.client);
    const outbox = new MongoOutbox(mongo.db, unitOfWork, {
      mapper: (aggregate, event) => {
        const mapped = restaurantOrderEventMapper(aggregate, event);
        // Unknown topic: the broker rejects the record.
        return mapped && aggregate.id.endsWith("-bad")
          ? { ...mapped, topic: `restaurants.missing-${randomUUID()}` }
          : mapped;
      },
    });
    const occurredAt = new Date();
    const add = (id: string) =>
      unitOfWork.run(() =>
        outbox.add({ type: "RestaurantOrder", id, version: 1 }, [
          {
            restaurantOrderId: id,
            orderId: "order-1",
            restaurantId: "rest-1",
            occurredAt,
            type: "RestaurantOrderReady",
            readyAt: occurredAt,
          },
        ] as never),
      );
    const prefix = randomUUID();
    await add(`${prefix}-bad`);
    await add(`${prefix}-good`);

    const failing = await KafkaPublisher.connect({
      enabled: true,
      brokers: brokers.split(","),
      clientId: "restaurants-relay-test-failing",
    });
    const publishWithTimeout = {
      publish: (message: Parameters<KafkaPublisher["publish"]>[0]) =>
        Promise.race([
          failing.publish(message),
          new Promise<never>((_, reject) =>
            setTimeout(() => reject(new Error("publish timed out")), 3000),
          ),
        ]),
    };
    try {
      const relay = new OutboxRelay({
        store: new MongoOutboxRelayStore(mongo.db, {
          leaseId: `lease-${prefix}`,
        }),
        publisher: publishWithTimeout,
        owner: "relay-hol",
        batchSize: 10,
        leaseTtlMs: 15_000,
        retentionMs: 7 * 86_400_000,
      });
      const result = await relay.cycle();
      assert.equal(result.published, 0);
      const collection = mongo.db.collection<OutboxDocument>(OUTBOX_COLLECTION);
      const bad = await collection.findOne({ aggregateId: `${prefix}-bad` });
      const good = await collection.findOne({ aggregateId: `${prefix}-good` });
      assert.equal(result.failedId, bad?._id);
      assert.equal(bad?.attempts, 1);
      assert.ok(bad?.lastError);
      assert.equal(good?.publishedAt, null, "blocked behind the failed record");
      assert.equal(good?.attempts, 0);
    } finally {
      await failing.close().catch(() => {});
    }
  });
});

async function consume(
  client: InstanceType<typeof kafka.KafkaJS.Kafka>,
  match: (message: Received) => boolean,
  count: number,
): Promise<Received[]> {
  const consumer = client.consumer({
    kafkaJS: {
      groupId: `restaurants-relay-test-${randomUUID()}`,
      fromBeginning: true,
    },
  });
  await consumer.connect();
  await consumer.subscribe({ topics: [RESTAURANT_ORDER_EVENTS_TOPIC] });
  const received: Received[] = [];
  try {
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(
        () =>
          reject(new Error(`received ${received.length}/${count} messages`)),
        30_000,
      );
      consumer
        .run({
          eachMessage: async ({ message }) => {
            const headers: Record<string, string> = {};
            for (const [name, value] of Object.entries(message.headers ?? {})) {
              headers[name] = String(Array.isArray(value) ? value[0] : value);
            }
            const candidate = {
              key: String(message.key),
              value: message.value ?? Buffer.alloc(0),
              headers,
            };
            if (!match(candidate)) return;
            received.push(candidate);
            if (received.length >= count) {
              clearTimeout(timer);
              resolve();
            }
          },
        })
        .catch(reject);
    });
  } finally {
    await consumer.disconnect();
  }
  return received;
}
