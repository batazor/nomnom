import assert from "node:assert/strict";
import test from "node:test";

import { ConcurrencyConflictError } from "../pkg/application/errors.ts";
import { MongoOutboxOutsideUnitOfWorkError } from "../pkg/infrastructure/repository/outbox.ts";
import {
  VersionedCollection,
  type VersionedDocument,
} from "../pkg/infrastructure/repository/versioned-collection.ts";
import { createTestDatabase, RecordedOutbox } from "./support/mongodb.ts";

const aggregate = { type: "Restaurant", id: "r-1", version: 1 };
const event = { type: "RestaurantOpened", occurredAt: new Date(0) };

type NamedDocument = VersionedDocument & { name: string };

const setup = async () => {
  const { db, unitOfWork } = await createTestDatabase();
  const store = new VersionedCollection<NamedDocument>({
    db,
    name: "foundation",
    aggregateType: "Restaurant",
    unitOfWork,
  });
  const outbox = new RecordedOutbox(db, unitOfWork);
  return { unitOfWork, store, outbox };
};

test("unit of work commits state and outbox together", async () => {
  const { unitOfWork, store, outbox } = await setup();

  await unitOfWork.run(async () => {
    await store.save("r-1", 0, { schemaVersion: 1, name: "Pho" });
    await outbox.add(aggregate, [event]);
  });

  assert.equal((await store.findOne({ _id: "r-1" }))?.version, 1);
  assert.deepEqual(await outbox.eventTypes(), ["RestaurantOpened"]);
});

test("unit of work rolls back state and outbox when work throws", async () => {
  const { unitOfWork, store, outbox } = await setup();
  await store.save("r-1", 0, { schemaVersion: 1, name: "Pho" });

  await assert.rejects(
    unitOfWork.run(async () => {
      await store.save("r-1", 1, { schemaVersion: 1, name: "Ramen" });
      await outbox.add(aggregate, [event]);
      throw new Error("boom");
    }),
    /boom/,
  );

  assert.deepEqual(await store.findOne({ _id: "r-1" }), {
    _id: "r-1",
    version: 1,
    schemaVersion: 1,
    name: "Pho",
  });
  assert.deepEqual(await outbox.records(), []);
});

test("nested scope joins the outer unit of work", async () => {
  const { unitOfWork, store } = await setup();

  await assert.rejects(
    unitOfWork.run(async () => {
      await unitOfWork.run(async () => {
        await store.save("r-1", 0, { schemaVersion: 1, name: "Pho" });
      });
      throw new Error("outer failure");
    }),
  );

  assert.equal(await store.findOne({ _id: "r-1" }), undefined);
});

test("outbox rejects records outside a unit of work", async () => {
  const { outbox } = await setup();

  await assert.rejects(
    outbox.add(aggregate, [event]),
    MongoOutboxOutsideUnitOfWorkError,
  );
});

test("versioned collection detects a stale version", async () => {
  const { store } = await setup();
  await store.save("r-1", 0, { schemaVersion: 1, name: "Pho" });

  await assert.rejects(
    store.save("r-1", 0, { schemaVersion: 1, name: "Ramen" }),
    ConcurrencyConflictError,
  );
  assert.equal(
    await store.save("r-1", 1, { schemaVersion: 1, name: "Ramen" }),
    2,
  );
});
