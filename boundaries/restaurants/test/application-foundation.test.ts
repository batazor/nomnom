import assert from "node:assert/strict";
import test from "node:test";

import { ConcurrencyConflictError } from "../pkg/application/errors.ts";
import { InMemoryDocumentStore } from "../pkg/infrastructure/memory/document-store.ts";
import {
  InMemoryOutbox,
  OutboxOutsideUnitOfWorkError,
} from "../pkg/infrastructure/memory/outbox.ts";
import { InMemoryUnitOfWork } from "../pkg/infrastructure/memory/unit-of-work.ts";

const aggregate = { type: "Restaurant", id: "r-1", version: 1 };
const event = { type: "RestaurantOpened", occurredAt: new Date(0) };

const setup = () => {
  const unitOfWork = new InMemoryUnitOfWork();
  const store = new InMemoryDocumentStore<{ name: string }>("Restaurant");
  unitOfWork.enlist(store);
  const outbox = new InMemoryOutbox(unitOfWork);
  return { unitOfWork, store, outbox };
};

test("unit of work commits state and outbox together", async () => {
  const { unitOfWork, store, outbox } = setup();

  await unitOfWork.run(async () => {
    store.save("r-1", 0, { name: "Pho" });
    await outbox.add(aggregate, [event]);
  });

  assert.equal(store.get("r-1")?.version, 1);
  assert.deepEqual(outbox.eventTypes(), ["RestaurantOpened"]);
  assert.equal(unitOfWork.commits, 1);
  assert.equal(unitOfWork.rollbacks, 0);
});

test("unit of work rolls back state and outbox when work throws", async () => {
  const { unitOfWork, store, outbox } = setup();
  store.save("r-1", 0, { name: "Pho" });

  await assert.rejects(
    unitOfWork.run(async () => {
      store.save("r-1", 1, { name: "Ramen" });
      await outbox.add(aggregate, [event]);
      throw new Error("boom");
    }),
    /boom/,
  );

  assert.deepEqual(store.get("r-1"), {
    id: "r-1",
    version: 1,
    document: { name: "Pho" },
  });
  assert.deepEqual(outbox.records, []);
  assert.equal(unitOfWork.rollbacks, 1);
});

test("nested scope joins the outer unit of work", async () => {
  const { unitOfWork, store } = setup();

  await assert.rejects(
    unitOfWork.run(async () => {
      await unitOfWork.run(async () => {
        store.save("r-1", 0, { name: "Pho" });
      });
      throw new Error("outer failure");
    }),
  );

  assert.equal(store.get("r-1"), undefined);
  assert.equal(unitOfWork.commits, 0);
  assert.equal(unitOfWork.rollbacks, 1);
});

test("outbox rejects records outside a unit of work", async () => {
  const { outbox } = setup();

  await assert.rejects(
    outbox.add(aggregate, [event]),
    OutboxOutsideUnitOfWorkError,
  );
});

test("document store detects a stale version", () => {
  const { store } = setup();
  store.save("r-1", 0, { name: "Pho" });

  assert.throws(
    () => store.save("r-1", 0, { name: "Ramen" }),
    ConcurrencyConflictError,
  );
  assert.equal(store.save("r-1", 1, { name: "Ramen" }), 2);
});

test("document store returns copies", () => {
  const { store } = setup();
  const document = { name: "Pho" };
  store.save("r-1", 0, document);
  document.name = "changed";

  const stored = store.get("r-1");
  assert.equal(stored?.document.name, "Pho");
});
