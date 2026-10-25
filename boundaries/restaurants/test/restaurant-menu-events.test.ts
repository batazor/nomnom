import assert from "node:assert/strict";
import test from "node:test";

import { fromBinary } from "@bufbuild/protobuf";

import {
  MenuItemAvailabilityChangedSchema,
  RestaurantClosedSchema,
  RestaurantOpenedSchema,
} from "../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import {
  type OutboxEventMapper,
  outboxEventId,
} from "../pkg/infrastructure/mongodb/outbox.ts";
import { outboxEventMapperByAggregate } from "../pkg/infrastructure/outbox/integration-event.ts";
import {
  MENU_AGGREGATE_TYPE,
  MENU_EVENTS_TOPIC,
  menuEventMapper,
} from "../slices/menu/infrastructure/integration/menu-events.ts";
import {
  RESTAURANT_AGGREGATE_TYPE,
  RESTAURANT_EVENTS_TOPIC,
  restaurantEventMapper,
} from "../slices/restaurant/infrastructure/integration/restaurant-events.ts";
import {
  RESTAURANT_ORDER_AGGREGATE_TYPE,
  RESTAURANT_ORDER_EVENTS_TOPIC,
  restaurantOrderEventMapper,
} from "../slices/restaurantorder/infrastructure/integration/restaurant-order-events.ts";
import { createMenuWorld } from "./support/menus.ts";
import type { RecordedOutboxRecord } from "./support/mongodb.ts";
import { owner } from "./support/restaurants.ts";

const at = new Date("2026-10-25T09:00:00.000Z");
const restaurant = { type: "Restaurant", id: "rest-1", version: 2 };
const menu = { type: "Menu", id: "menu-1", version: 5 };

/** Same composition as di/restaurants.ts. */
const processMapper: OutboxEventMapper = outboxEventMapperByAggregate({
  [RESTAURANT_AGGREGATE_TYPE]: restaurantEventMapper,
  [MENU_AGGREGATE_TYPE]: menuEventMapper,
  [RESTAURANT_ORDER_AGGREGATE_TYPE]: restaurantOrderEventMapper,
});

const event = (fields: Record<string, unknown>) =>
  ({
    occurredAt: at,
    ...fields,
  }) as unknown as Parameters<OutboxEventMapper>[1];

test("RestaurantOpened and RestaurantClosed go to restaurant-events with restaurant_id", () => {
  for (const [type, schema] of [
    ["RestaurantOpened", RestaurantOpenedSchema],
    ["RestaurantClosed", RestaurantClosedSchema],
  ] as const) {
    const mapped = restaurantEventMapper(
      restaurant,
      event({ type, restaurantId: "rest-1" }),
    );
    assert.ok(mapped, type);
    assert.equal(mapped.topic, RESTAURANT_EVENTS_TOPIC);
    assert.equal(mapped.topic, "restaurants.v1.restaurant-events");
    assert.equal(mapped.eventType, `nomnom.restaurants.v1.${type}`);
    assert.equal(fromBinary(schema, mapped.payload).restaurantId, "rest-1");
  }
});

test("MenuItemAvailabilityChanged goes to menu-events with menu, restaurant, item and value", () => {
  const mapped = menuEventMapper(
    menu,
    event({
      type: "MenuItemAvailabilityChanged",
      menuId: "menu-1",
      restaurantId: "rest-1",
      menuItemId: "pho",
      isAvailable: false,
    }),
  );
  assert.ok(mapped);
  assert.equal(mapped.topic, MENU_EVENTS_TOPIC);
  assert.equal(mapped.topic, "restaurants.v1.menu-events");
  assert.equal(
    mapped.eventType,
    "nomnom.restaurants.v1.MenuItemAvailabilityChanged",
  );
  const message = fromBinary(MenuItemAvailabilityChangedSchema, mapped.payload);
  assert.equal(message.menuId, "menu-1");
  assert.equal(message.restaurantId, "rest-1");
  assert.equal(message.menuItemId, "pho");
  assert.equal(message.available, false);
});

test("internal Restaurant and Menu facts are not published", () => {
  for (const type of [
    "RestaurantRegistered",
    "RestaurantRenamed",
    "RestaurantAddressChanged",
    "toString",
    "constructor",
  ]) {
    assert.equal(
      restaurantEventMapper(restaurant, event({ type })),
      undefined,
      type,
    );
  }
  for (const type of [
    "MenuCreated",
    "MenuItemAdded",
    "MenuItemUpdated",
    "hasOwnProperty",
  ]) {
    assert.equal(menuEventMapper(menu, event({ type })), undefined, type);
  }
});

test("a mapper ignores the same event type of another aggregate", () => {
  const opened = event({ type: "RestaurantOpened", restaurantId: "rest-1" });
  assert.equal(restaurantEventMapper(menu, opened), undefined);
  assert.equal(
    menuEventMapper(
      restaurant,
      event({ type: "MenuItemAvailabilityChanged", isAvailable: true }),
    ),
    undefined,
  );
});

test("the process mapper dispatches by aggregate type, one topic per aggregate", () => {
  assert.equal(
    processMapper(
      restaurant,
      event({ type: "RestaurantOpened", restaurantId: "rest-1" }),
    )?.topic,
    RESTAURANT_EVENTS_TOPIC,
  );
  assert.equal(
    processMapper(
      menu,
      event({
        type: "MenuItemAvailabilityChanged",
        menuId: "menu-1",
        restaurantId: "rest-1",
        menuItemId: "pho",
        isAvailable: true,
      }),
    )?.topic,
    MENU_EVENTS_TOPIC,
  );
  assert.equal(
    processMapper(
      { type: "RestaurantOrder", id: "ro-1", version: 2 },
      event({ type: "PreparationStarted", orderId: "order-1" }),
    )?.topic,
    RESTAURANT_ORDER_EVENTS_TOPIC,
  );
  for (const type of ["Unknown", "toString", "__proto__"]) {
    assert.equal(
      processMapper(
        { ...restaurant, type },
        event({ type: "RestaurantOpened", restaurantId: "rest-1" }),
      ),
      undefined,
      type,
    );
  }
});

/** Outbox records the process would write for the captured port calls. */
const recordsOf = (records: readonly RecordedOutboxRecord[]) =>
  records.flatMap(({ aggregate, event }) => {
    const mapped = processMapper(aggregate, event);
    return mapped === undefined
      ? []
      : [
          {
            eventId: outboxEventId(aggregate, mapped.eventType),
            eventType: mapped.eventType,
            aggregateType: aggregate.type,
            aggregateId: aggregate.id,
            aggregateVersion: aggregate.version,
            topic: mapped.topic,
            payload: mapped.payload,
          },
        ];
  });

test("Open and Close Restaurant produce restaurant-events records keyed by the restaurant", async () => {
  const world = await createMenuWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar", { open: false });
  await world.open.execute({ subject: owner, restaurantId });
  await world.close.execute({ subject: owner, restaurantId });

  const records = recordsOf(await world.outbox.records());
  assert.deepEqual(
    records.map(({ payload: _, ...record }) => record),
    [
      {
        eventId: outboxEventId(
          { type: "Restaurant", id: restaurantId, version: 2 },
          "nomnom.restaurants.v1.RestaurantOpened",
        ),
        eventType: "nomnom.restaurants.v1.RestaurantOpened",
        aggregateType: "Restaurant",
        aggregateId: restaurantId,
        aggregateVersion: 2,
        topic: RESTAURANT_EVENTS_TOPIC,
      },
      {
        eventId: outboxEventId(
          { type: "Restaurant", id: restaurantId, version: 3 },
          "nomnom.restaurants.v1.RestaurantClosed",
        ),
        eventType: "nomnom.restaurants.v1.RestaurantClosed",
        aggregateType: "Restaurant",
        aggregateId: restaurantId,
        aggregateVersion: 3,
        topic: RESTAURANT_EVENTS_TOPIC,
      },
    ],
  );
  const closed = records[1];
  assert.ok(closed);
  assert.equal(
    fromBinary(RestaurantClosedSchema, closed.payload).restaurantId,
    restaurantId,
  );
});

test("Set Availability and Update Menu Item produce menu-events records keyed by the menu", async () => {
  const world = await createMenuWorld();
  const restaurantId = await world.givenRestaurantWithMenu();
  const menuItemId = await world.givenMenuItem(restaurantId, "Pho");

  const off = await world.setAvailability.execute({
    subject: owner,
    restaurantId,
    menuItemId,
    isAvailable: false,
  });
  // A repeat of the current value is a no-op without a record.
  await world.setAvailability.execute({
    subject: owner,
    restaurantId,
    menuItemId,
    isAvailable: false,
  });
  // Renaming alone is internal; renaming with availability publishes.
  await world.updateMenuItem.execute({
    subject: owner,
    restaurantId,
    menuItemId,
    name: "Pho Bo",
  });
  const on = await world.updateMenuItem.execute({
    subject: owner,
    restaurantId,
    menuItemId,
    name: "Pho Ga",
    isAvailable: true,
  });

  const records = recordsOf(await world.outbox.records()).filter(
    (record) => record.aggregateType === "Menu",
  );
  assert.deepEqual(
    records.map((record) => ({
      topic: record.topic,
      aggregateId: record.aggregateId,
      aggregateVersion: record.aggregateVersion,
      ...fromBinary(MenuItemAvailabilityChangedSchema, record.payload),
    })),
    [
      { version: off.version, available: false },
      { version: on.version, available: true },
    ].map(({ version, available }) => ({
      topic: MENU_EVENTS_TOPIC,
      aggregateId: off.menuId,
      aggregateVersion: version,
      $typeName: "nomnom.restaurants.v1.MenuItemAvailabilityChanged",
      menuId: off.menuId,
      restaurantId,
      menuItemId,
      available,
    })),
  );
  assert.notEqual(records[0]?.eventId, records[1]?.eventId);
});
