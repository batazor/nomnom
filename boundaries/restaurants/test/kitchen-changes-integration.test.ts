import assert from "node:assert/strict";
import test from "node:test";

import {
  OUTBOX_COLLECTION,
  type OutboxDocument,
} from "../pkg/infrastructure/repository/outbox.ts";
import {
  KITCHEN_CHANGES_TOPIC,
  kitchenChangeNotifier,
} from "../slices/restaurantorder/infrastructure/integration/kitchen-changes.ts";
import { createTestDatabase, RecordedOutbox } from "./support/database.ts";

const occurredAt = new Date("2026-10-25T12:00:00Z");

// ADR 36: the notification is the Kitchen entity key in JSON, written in the
// Unit of Work of the change, on its own topic next to Integration Events.
test("kitchen change notification is the Kitchen key in the outbox", async () => {
  const { db, unitOfWork } = await createTestDatabase();
  const notify = kitchenChangeNotifier(
    unitOfWork,
    new RecordedOutbox(db, unitOfWork),
    () => occurredAt,
  );

  const version = await notify({
    type: "RestaurantOrder",
    id: "ro-1",
    restaurantId: "kitchen-8",
    save: async () => 2,
  });

  assert.equal(version, 2);

  const [record] = await db
    .collection<OutboxDocument>(OUTBOX_COLLECTION)
    .find({})
    .toArray();
  assert.ok(record);
  assert.equal(record.topic, KITCHEN_CHANGES_TOPIC);
  assert.equal(record.eventType, "KitchenChanged");
  assert.equal(record.aggregateId, "ro-1");
  assert.deepEqual(
    JSON.parse(new TextDecoder().decode(record.payload.buffer)),
    { __typename: "Kitchen", restaurantId: "kitchen-8" },
  );
});

test("a failed write leaves no kitchen change notification", async () => {
  const { db, unitOfWork } = await createTestDatabase();
  const notify = kitchenChangeNotifier(
    unitOfWork,
    new RecordedOutbox(db, unitOfWork),
    () => occurredAt,
  );

  await assert.rejects(
    notify({
      type: "Restaurant",
      id: "kitchen-8",
      restaurantId: "kitchen-8",
      save: () => Promise.reject(new Error("conflict")),
    }),
    /conflict/,
  );
  assert.equal(await db.collection(OUTBOX_COLLECTION).countDocuments(), 0);
});
