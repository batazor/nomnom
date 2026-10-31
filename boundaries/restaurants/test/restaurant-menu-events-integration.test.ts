import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";
import { fromBinary } from "@bufbuild/protobuf";
import {
  MenuItemAvailabilityChangedSchema,
  RestaurantOpenedSchema,
} from "../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import { outboxEventMapperByAggregate } from "../infrastructure/outbox/integration-event.ts";
import {
  connectDatabase,
  type DatabaseConnection,
} from "../infrastructure/repository/client.ts";
import { migrateRestaurantsDatabase } from "../infrastructure/repository/migrate.ts";
import {
  DatabaseOutbox,
  OUTBOX_COLLECTION,
  type OutboxDocument,
  outboxEventId,
} from "../infrastructure/repository/outbox.ts";
import { DatabaseUnitOfWork } from "../infrastructure/repository/unit-of-work.ts";
import type { Subject } from "../pkg/authz/subject.ts";
import { SetMenuItemAvailabilityHandler } from "../slices/menu/applications/set-item-availability/set-item-availability.ts";
import { Menu } from "../slices/menu/domains/menu/menu.ts";
import {
  MENU_AGGREGATE_TYPE,
  MENU_EVENTS_TOPIC,
  menuEventMapper,
} from "../slices/menu/infrastructure/integration/menu-events.ts";
import {
  createMenuCollection,
  DatabaseMenuRepository,
} from "../slices/menu/infrastructure/repository/menus.ts";
import { OpenRestaurantHandler } from "../slices/restaurant/applications/open/open.ts";
import { Restaurant } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import { Money } from "../slices/restaurant/domains/restaurant/vo/money.ts";
import {
  RESTAURANT_AGGREGATE_TYPE,
  RESTAURANT_EVENTS_TOPIC,
  restaurantEventMapper,
} from "../slices/restaurant/infrastructure/integration/restaurant-events.ts";
import {
  createRestaurantCollection,
  DatabaseRestaurantRepository,
} from "../slices/restaurant/infrastructure/repository/restaurants.ts";
import {
  RESTAURANT_ORDER_AGGREGATE_TYPE,
  restaurantOrderEventMapper,
} from "../slices/restaurantorder/infrastructure/integration/restaurant-order-events.ts";
import { databaseUri, noKitchenChanges } from "./support/database.ts";
import { TestMenuAccessControl } from "./support/menus.ts";

// Open Restaurant and Set Menu Item Availability → Outbox records in the CDC
// contract (ADR 37): Debezium MongoEventRouter takes the topic, the key
// (`aggregateId`), the value (`payload`) and the Event Envelope headers from
// these fields only. MongoDB is the testcontainer of the global setup
// (test/support/global-containers.ts).
const uri = databaseUri();

const owner: Subject = { type: "user", id: "owner" };

describe("Restaurant and Menu Integration Events are written to the Outbox", () => {
  let database: DatabaseConnection;

  before(async () => {
    database = await connectDatabase({
      uri,
      database: `restaurants_events_${randomUUID().slice(0, 8)}`,
      timeoutMs: 5000,
    });
    await migrateRestaurantsDatabase(database.db);
  });

  after(async () => {
    if (database === undefined) return;
    await database.db.dropDatabase();
    await database.close();
  });

  test("Open Restaurant and Set Availability are recorded for their streams", async () => {
    const unitOfWork = new DatabaseUnitOfWork(database.client);
    const traceparent =
      "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01";
    const outbox = new DatabaseOutbox(database.db, unitOfWork, {
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
    const restaurants = new DatabaseRestaurantRepository(
      createRestaurantCollection(database.db, unitOfWork),
      noKitchenChanges,
    );
    const menus = new DatabaseMenuRepository(
      createMenuCollection(database.db, unitOfWork),
    );

    const restaurantId = `r-${randomUUID()}`;
    await restaurants.save(
      Restaurant.register({
        id: restaurantId,
        name: "Pho House",
        ownerId: owner.id,
        marketId: "berlin",
        timeZone: "Europe/Berlin",
        registeredAt: now,
      }),
    );
    const menuId = `m-${randomUUID()}`;
    const menu = Menu.create({
      id: menuId,
      restaurantId,
      marketId: "berlin",
      createdAt: now,
    });
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

    const collection =
      database.db.collection<OutboxDocument>(OUTBOX_COLLECTION);
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
    assert.equal(
      fromBinary(RestaurantOpenedSchema, restaurantRecord.payload.buffer)
        .marketId,
      "berlin",
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
    assert.equal(payload.marketId, "berlin");
  });
});
