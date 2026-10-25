import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";
import { fromBinary } from "@bufbuild/protobuf";
import {
  MenuItemAvailabilityChangedSchema,
  RestaurantOpenedSchema,
} from "../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import { migrateRestaurantsDatabase } from "../infrastructure/mongodb/collections.ts";
import type { Subject } from "../pkg/application/subject.ts";
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
import { MongoUnitOfWork } from "../pkg/infrastructure/mongodb/unit-of-work.ts";
import { outboxEventMapperByAggregate } from "../pkg/infrastructure/outbox/integration-event.ts";
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
import { TestMenuAccessControl } from "./support/menus.ts";
import { mongoUri, noKitchenChanges } from "./support/mongodb.ts";

// Open Restaurant and Set Menu Item Availability → Outbox records in the CDC
// contract (ADR 37): Debezium MongoEventRouter takes the topic, the key
// (`aggregateId`), the value (`payload`) and the Event Envelope headers from
// these fields only. MongoDB is the testcontainer of the global setup
// (test/support/global-containers.ts).
const uri = mongoUri();

const owner: Subject = { type: "user", id: "owner" };

describe("Restaurant and Menu Integration Events are written to the Outbox", () => {
  let mongo: MongoDbConnection;

  before(async () => {
    mongo = await connectMongoDb({
      enabled: true,
      uri,
      database: `restaurants_events_${randomUUID().slice(0, 8)}`,
      timeoutMs: 5000,
    });
    await migrateRestaurantsDatabase(mongo.db);
  });

  after(async () => {
    if (mongo === undefined) return;
    await mongo.db.dropDatabase();
    await mongo.close();
  });

  test("Open Restaurant and Set Availability are recorded for their streams", async () => {
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
    const records = await collection.find().sort({ createdAt: 1 }).toArray();
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

    const openedId = outboxEventId(
      { type: "Restaurant", id: restaurantId, version: opened.version },
      "nomnom.restaurants.v1.RestaurantOpened",
    );
    const availabilityId = outboxEventId(
      { type: "Menu", id: menuId, version: unavailable.version },
      "nomnom.restaurants.v1.MenuItemAvailabilityChanged",
    );
    const [restaurantRecord, menuRecord] = records;
    assert.ok(restaurantRecord, "RestaurantOpened for restaurant-events");
    assert.equal(restaurantRecord.eventId, openedId);
    assert.deepEqual(restaurantRecord.occurredAt, now);
    assert.equal(restaurantRecord.traceparent, traceparent);
    assert.equal(
      fromBinary(RestaurantOpenedSchema, restaurantRecord.payload.buffer)
        .restaurantId,
      restaurantId,
    );

    assert.ok(menuRecord, "MenuItemAvailabilityChanged for menu-events");
    assert.equal(menuRecord.eventId, availabilityId);
    assert.deepEqual(menuRecord.occurredAt, now);
    assert.equal(menuRecord.traceparent, traceparent);
    const payload = fromBinary(
      MenuItemAvailabilityChangedSchema,
      menuRecord.payload.buffer,
    );
    assert.equal(payload.menuId, menuId);
    assert.equal(payload.restaurantId, restaurantId);
    assert.equal(payload.menuItemId, "pho");
    assert.equal(payload.available, false);
  });
});
