import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";
import { fromBinary } from "@bufbuild/protobuf";
import kafka from "@confluentinc/kafka-javascript";
import {
  MenuItemAvailabilityChangedSchema,
  RestaurantOpenedSchema,
} from "../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import { migrateRestaurantsDatabase } from "../infrastructure/mongodb/collections.ts";
import type { Subject } from "../pkg/application/subject.ts";
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
import { outboxEventMapperByAggregate } from "../pkg/infrastructure/outbox/integration-event.ts";
import { OutboxRelay } from "../pkg/infrastructure/outbox/relay.ts";
import { SetMenuItemAvailabilityHandler } from "../slices/menu/applications/set-menu-item-availability/set-menu-item-availability.ts";
import { Menu } from "../slices/menu/domains/menu/menu.ts";
import { Money } from "../slices/menu/domains/menu/vo/money.ts";
import {
  MENU_AGGREGATE_TYPE,
  MENU_EVENTS_TOPIC,
  menuEventMapper,
} from "../slices/menu/infrastructure/integration/menu-events.ts";
import {
  createMenuCollection,
  MongoMenuRepository,
} from "../slices/menu/infrastructure/mongodb/menus.ts";
import { OpenRestaurantHandler } from "../slices/restaurant/applications/open-restaurant/open-restaurant.ts";
import { Restaurant } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import {
  RESTAURANT_AGGREGATE_TYPE,
  RESTAURANT_EVENTS_TOPIC,
  restaurantEventMapper,
} from "../slices/restaurant/infrastructure/integration/restaurant-events.ts";
import {
  createRestaurantCollection,
  MongoRestaurantRepository,
} from "../slices/restaurant/infrastructure/mongodb/restaurants.ts";
import {
  RESTAURANT_ORDER_AGGREGATE_TYPE,
  restaurantOrderEventMapper,
} from "../slices/restaurantorder/infrastructure/integration/restaurant-order-events.ts";
import { kafkaBrokers } from "./support/global-containers.ts";
import { TestMenuAccessControl } from "./support/menus.ts";
import { mongoUri, noKitchenChanges } from "./support/mongodb.ts";

// Open Restaurant and Set Menu Item Availability → Outbox → restaurants-relay
// → Kafka against real infrastructure, for example
//   docker run -d -p 9092:9092 apache/kafka:4.1.0
// MongoDB is the testcontainer of the global setup (test/support/global-containers.ts).
const uri = mongoUri();
const brokers = kafkaBrokers();

type Received = {
  topic: string;
  key: string;
  value: Buffer;
  headers: Record<string, string>;
};

const owner: Subject = { type: "user", id: "owner" };

describe("Restaurant and Menu Integration Events reach Kafka", () => {
  let mongo: MongoDbConnection;
  let publisher: KafkaPublisher;
  const client = new kafka.KafkaJS.Kafka({
    kafkaJS: {
      brokers: brokers,
      logLevel: kafka.KafkaJS.logLevel.ERROR,
    },
  });

  before(async () => {
    mongo = await connectMongoDb({
      enabled: true,
      uri,
      database: `restaurants_events_${randomUUID().slice(0, 8)}`,
      timeoutMs: 5000,
    });
    await migrateRestaurantsDatabase(mongo.db);
    const admin = client.admin();
    await admin.connect();
    const topics = await admin.listTopics();
    const missing = [RESTAURANT_EVENTS_TOPIC, MENU_EVENTS_TOPIC].filter(
      (topic) => !topics.includes(topic),
    );
    if (missing.length > 0) {
      await admin.createTopics({
        topics: missing.map((topic) => ({ topic, numPartitions: 3 })),
      });
    }
    await admin.disconnect();
    publisher = await KafkaPublisher.connect({
      enabled: true,
      brokers: brokers,
      clientId: "restaurants-events-test",
    });
  });

  after(async () => {
    await publisher?.close();
    if (mongo === undefined) return;
    await mongo.db.dropDatabase();
    await mongo.close();
  });

  test("Open Restaurant and Set Availability are published on their streams", async () => {
    const unitOfWork = new MongoUnitOfWork(mongo.client);
    const traceparent =
      "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01";
    const outbox = new MongoOutbox(mongo.db, unitOfWork, {
      // Same composition as di/restaurants.ts.
      mapper: outboxEventMapperByAggregate({
        [RESTAURANT_AGGREGATE_TYPE]: restaurantEventMapper,
        [MENU_AGGREGATE_TYPE]: menuEventMapper,
        [RESTAURANT_ORDER_AGGREGATE_TYPE]: restaurantOrderEventMapper,
      }),
      metadata: () => ({ traceparent }),
    });
    const now = new Date("2026-10-25T09:00:00.120Z");
    const clock = { now: () => now };
    const restaurants = new MongoRestaurantRepository(
      createRestaurantCollection(mongo.db, unitOfWork),
      noKitchenChanges,
    );
    const menus = new MongoMenuRepository(
      createMenuCollection(mongo.db, unitOfWork),
    );

    const restaurantId = `r-${randomUUID()}`;
    await restaurants.save(
      Restaurant.register({
        id: restaurantId,
        name: "Pho House",
        ownerId: owner.id,
        address: "1 Pho Street",
        registeredAt: now,
      }),
    );
    const menuId = `m-${randomUUID()}`;
    const menu = Menu.create({ id: menuId, restaurantId, createdAt: now });
    menu.addItem({
      id: "pho",
      name: "Pho",
      price: Money.from({ amount: "4.50", currency: "USD" }),
      isAvailable: true,
      addedAt: now,
    });
    await menus.save(menu);

    const opened = await new OpenRestaurantHandler({
      accessControl: {
        canRegisterRestaurant: async () => false,
        canManageRestaurant: async (subject, id) =>
          subject.id === owner.id && id === restaurantId,
      },
      unitOfWork,
      restaurants,
      outbox,
      clock,
    }).execute({ subject: owner, restaurantId });
    const unavailable = await new SetMenuItemAvailabilityHandler({
      accessControl: new TestMenuAccessControl().grantManager(
        owner,
        restaurantId,
      ),
      unitOfWork,
      menus,
      outbox,
      clock,
    }).execute({
      subject: owner,
      restaurantId,
      menuItemId: "pho",
      isAvailable: false,
    });

    const collection = mongo.db.collection<OutboxDocument>(OUTBOX_COLLECTION);
    const records = await collection.find().sort({ seq: 1 }).toArray();
    assert.deepEqual(
      records.map((record) => [
        record.topic,
        record.eventType,
        record.aggregateType,
        record.aggregateId,
        record.aggregateVersion,
      ]),
      [
        [
          RESTAURANT_EVENTS_TOPIC,
          "nomnom.restaurants.v1.RestaurantOpened",
          "Restaurant",
          restaurantId,
          opened.version,
        ],
        [
          MENU_EVENTS_TOPIC,
          "nomnom.restaurants.v1.MenuItemAvailabilityChanged",
          "Menu",
          menuId,
          unavailable.version,
        ],
      ],
      "registration and menu creation are internal facts",
    );

    const relay = new OutboxRelay({
      store: new MongoOutboxRelayStore(mongo.db),
      publisher,
      owner: "relay-events",
      batchSize: 10,
      leaseTtlMs: 15_000,
      retentionMs: 7 * 86_400_000,
    });
    assert.deepEqual(await relay.cycle(), {
      leased: true,
      published: 2,
      deleted: 0,
    });
    await relay.release();

    const openedId = outboxEventId(
      { type: "Restaurant", id: restaurantId, version: opened.version },
      "nomnom.restaurants.v1.RestaurantOpened",
    );
    const availabilityId = outboxEventId(
      { type: "Menu", id: menuId, version: unavailable.version },
      "nomnom.restaurants.v1.MenuItemAvailabilityChanged",
    );
    const received = await consume(
      client,
      [RESTAURANT_EVENTS_TOPIC, MENU_EVENTS_TOPIC],
      (message) =>
        [openedId, availabilityId].includes(message.headers.event_id ?? ""),
      2,
    );
    const byTopic = new Map(
      received.map((message) => [message.topic, message]),
    );

    const restaurantMessage = byTopic.get(RESTAURANT_EVENTS_TOPIC);
    assert.ok(restaurantMessage, "RestaurantOpened on restaurant-events");
    assert.equal(restaurantMessage.key, restaurantId);
    assert.deepEqual(restaurantMessage.headers, {
      _watermill_message_uuid: openedId,
      event_id: openedId,
      event_type: "nomnom.restaurants.v1.RestaurantOpened",
      occurred_at: "2026-10-25T09:00:00.12Z",
      aggregate_type: "Restaurant",
      aggregate_id: restaurantId,
      aggregate_version: String(opened.version),
      traceparent,
    });
    assert.equal(
      fromBinary(RestaurantOpenedSchema, restaurantMessage.value).restaurantId,
      restaurantId,
    );

    const menuMessage = byTopic.get(MENU_EVENTS_TOPIC);
    assert.ok(menuMessage, "MenuItemAvailabilityChanged on menu-events");
    assert.equal(menuMessage.key, menuId);
    assert.deepEqual(menuMessage.headers, {
      _watermill_message_uuid: availabilityId,
      event_id: availabilityId,
      event_type: "nomnom.restaurants.v1.MenuItemAvailabilityChanged",
      occurred_at: "2026-10-25T09:00:00.12Z",
      aggregate_type: "Menu",
      aggregate_id: menuId,
      aggregate_version: String(unavailable.version),
      traceparent,
    });
    const payload = fromBinary(
      MenuItemAvailabilityChangedSchema,
      menuMessage.value,
    );
    assert.equal(payload.menuId, menuId);
    assert.equal(payload.restaurantId, restaurantId);
    assert.equal(payload.menuItemId, "pho");
    assert.equal(payload.available, false);
  });
});

async function consume(
  client: InstanceType<typeof kafka.KafkaJS.Kafka>,
  topics: string[],
  match: (message: Received) => boolean,
  count: number,
): Promise<Received[]> {
  const consumer = client.consumer({
    kafkaJS: {
      groupId: `restaurants-events-test-${randomUUID()}`,
      fromBeginning: true,
    },
  });
  await consumer.connect();
  await consumer.subscribe({ topics });
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
          eachMessage: async ({ topic, message }) => {
            const headers: Record<string, string> = {};
            for (const [name, value] of Object.entries(message.headers ?? {})) {
              headers[name] = String(Array.isArray(value) ? value[0] : value);
            }
            const candidate = {
              topic,
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
