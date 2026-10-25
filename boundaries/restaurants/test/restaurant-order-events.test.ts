import assert from "node:assert/strict";
import test from "node:test";

import { fromBinary } from "@bufbuild/protobuf";
import { timestampDate } from "@bufbuild/protobuf/wkt";

import {
  PreparationDelayedSchema,
  PreparationFailedSchema,
  RestaurantOrderAcceptedSchema,
  RestaurantOrderCancelledSchema,
  RestaurantOrderRejectedSchema,
} from "../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import { outboxEventId, uuidV5 } from "../pkg/infrastructure/mongodb/outbox.ts";
import { RestaurantOrderState } from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import {
  RESTAURANT_ORDER_EVENTS_TOPIC,
  restaurantOrderEventMapper,
} from "../slices/restaurantorder/infrastructure/integration/restaurant-order-events.ts";

const aggregate = { type: "RestaurantOrder", id: "ro-1", version: 3 };
const base = {
  restaurantOrderId: "ro-1",
  orderId: "order-1",
  restaurantId: "rest-1",
  occurredAt: new Date("2026-10-25T09:00:00.000Z"),
};
const at = new Date("2026-10-25T09:30:00.000Z");

const map = (event: Record<string, unknown>) => {
  const mapped = restaurantOrderEventMapper(
    aggregate,
    event as unknown as Parameters<typeof restaurantOrderEventMapper>[1],
  );
  assert.ok(mapped, `expected ${String(event.type)} to be published`);
  assert.equal(mapped.topic, RESTAURANT_ORDER_EVENTS_TOPIC);
  return mapped;
};

test("maps RestaurantOrderAccepted to its contract", () => {
  const mapped = map({
    ...base,
    type: "RestaurantOrderAccepted",
    acceptedAt: at,
    expectedReadyAt: at,
  });
  assert.equal(
    mapped.eventType,
    "nomnom.restaurants.v1.RestaurantOrderAccepted",
  );
  assert.equal(
    fromBinary(RestaurantOrderAcceptedSchema, mapped.payload).orderId,
    "order-1",
  );
});

test("RestaurantOrderRejected carries an empty reason when none was given", () => {
  const mapped = map({
    ...base,
    type: "RestaurantOrderRejected",
    stage: RestaurantOrderState.Received,
    reason: undefined,
  });
  const message = fromBinary(RestaurantOrderRejectedSchema, mapped.payload);
  assert.equal(message.orderId, "order-1");
  assert.equal(message.reason, "");
});

test("PreparationDelayed carries the new expected ready time", () => {
  const mapped = map({
    ...base,
    type: "PreparationDelayed",
    expectedReadyAt: at,
    previousExpectedReadyAt: undefined,
    reason: "oven",
  });
  const message = fromBinary(PreparationDelayedSchema, mapped.payload);
  assert.ok(message.expectedReadyAt);
  assert.equal(timestampDate(message.expectedReadyAt).getTime(), at.getTime());
});

test("RestaurantOrderFailed is published as PreparationFailed", () => {
  const mapped = map({
    ...base,
    type: "RestaurantOrderFailed",
    stage: RestaurantOrderState.Preparing,
    reason: "no gas",
  });
  assert.equal(mapped.eventType, "nomnom.restaurants.v1.PreparationFailed");
  assert.equal(
    fromBinary(PreparationFailedSchema, mapped.payload).reason,
    "no gas",
  );
});

test("RestaurantOrderCancelled is published with its stage fact", () => {
  const mapped = map({
    ...base,
    type: "RestaurantOrderCancelled",
    stage: RestaurantOrderState.Preparing,
    preparationStarted: true,
    reason: undefined,
  });
  const message = fromBinary(RestaurantOrderCancelledSchema, mapped.payload);
  assert.equal(message.orderId, "order-1");
  assert.equal(message.reason, "");
  assert.equal(message.preparationStarted, true);
});

test("PreparationStarted and RestaurantOrderReady are published", () => {
  assert.equal(
    map({ ...base, type: "PreparationStarted", preparationStartedAt: at })
      .eventType,
    "nomnom.restaurants.v1.PreparationStarted",
  );
  assert.equal(
    map({ ...base, type: "RestaurantOrderReady", readyAt: at }).eventType,
    "nomnom.restaurants.v1.RestaurantOrderReady",
  );
});

test("events without a contract are not published", () => {
  for (const type of [
    "RestaurantOrderReceived",
    "RestaurantOrderQueued",
    "RestaurantOrderAssembled",
    "RestaurantOpened",
    "MenuItemAvailabilityChanged",
    "toString",
  ]) {
    assert.equal(
      restaurantOrderEventMapper(aggregate, { type, occurredAt: at }),
      undefined,
      type,
    );
  }
  // Same event type of another aggregate is not a Kitchen Order fact.
  assert.equal(
    restaurantOrderEventMapper({ ...aggregate, type: "Restaurant" }, {
      ...base,
      type: "RestaurantOrderAccepted",
    } as never),
    undefined,
  );
});

test("event_id is the UUIDv5 of the aggregate version and event type", () => {
  // RFC 4122 test vector: uuid5(NAMESPACE_DNS, "python.org").
  assert.equal(
    uuidV5("python.org", "6ba7b810-9dad-11d1-80b4-00c04fd430c8"),
    "886313e1-3b8a-5372-9b90-0c9aee199e5d",
  );
  const id = outboxEventId(
    aggregate,
    "nomnom.restaurants.v1.PreparationStarted",
  );
  // Python uuid.uuid5 of the same name: same id in any publisher language.
  assert.equal(id, "d9a97866-e51b-582f-b439-2fee9368e429");
  assert.equal(
    id,
    uuidV5(
      "RestaurantOrder:ro-1:3:nomnom.restaurants.v1.PreparationStarted",
      "8f1d3c2a-5b7e-4a90-9c61-2e4f7b8d0a13",
    ),
  );
  assert.match(
    id,
    /^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
  );
  assert.notEqual(
    id,
    outboxEventId(
      { ...aggregate, version: 4 },
      "nomnom.restaurants.v1.PreparationStarted",
    ),
  );
});
