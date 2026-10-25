import assert from "node:assert/strict";
import test from "node:test";
import { FixedClock } from "../pkg/infrastructure/memory/clock.ts";
import type { StoredRestaurantSnapshot } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import {
  createRestaurantStore,
  InMemoryRestaurantMemberships,
} from "../slices/restaurant/infrastructure/memory/restaurants.ts";
import { RelationshipReconciler } from "../slices/restaurant/infrastructure/reconcile/reconcile.ts";
import { FakeRelationshipsClient } from "./support/relationships.ts";

const GRACE = 10 * 60_000;

const setup = (
  restaurants: Array<Partial<StoredRestaurantSnapshot> & { id: string }>,
  tuples: string[],
  options: { pageSize?: number; resourceIdPrefix?: string } = {},
) => {
  const store = createRestaurantStore();
  for (const restaurant of restaurants) {
    store.save(restaurant.id, 0, {
      name: "Pho",
      ownerId: "owner",
      isOpen: false,
      ...restaurant,
    });
  }
  const client = new FakeRelationshipsClient(...tuples);
  const clock = new FixedClock(new Date(Date.UTC(2026, 9, 25)));
  const logs: string[] = [];
  const reconciler = new RelationshipReconciler({
    client,
    restaurants: new InMemoryRestaurantMemberships(store),
    graceMs: GRACE,
    pageSize: options.pageSize ?? 500,
    timeoutMs: 1000,
    clock,
    resourceIdPrefix: options.resourceIdPrefix,
    log: (message) => logs.push(message),
  });
  return { store, client, clock, reconciler, logs };
};

test("AC-478 restores missing owner and staff relationships immediately", async () => {
  const { client, reconciler, logs, store } = setup(
    [{ id: "r-1", ownerId: "alice", staff: ["bob"] }],
    [],
  );

  const stats = await reconciler.reconcile();

  assert.deepEqual(stats, {
    checked: 0,
    orphansPending: 0,
    orphansDeleted: 0,
    missingRepaired: 2,
    unrepairable: 0,
  });
  assert.ok(client.has("restaurant:r-1#owner@user:alice"));
  assert.ok(client.has("restaurant:r-1#staff@user:bob"));
  // FR-447: the revision of the last repairing TOUCH is stored.
  const stored = store.get("r-1");
  assert.equal(stored?.document.authorizationRevision, "zed-2");
  assert.equal(stored?.version, 3);
  assert.match(
    logs.at(-1) ?? "",
    /checked=0 orphans_pending=0 orphans_deleted=0 missing_repaired=2 unrepairable=0/,
  );
});

test("consistent relationships are checked and left alone", async () => {
  const { client, reconciler } = setup(
    [{ id: "r-1", ownerId: "alice", staff: ["bob"] }],
    ["restaurant:r-1#owner@user:alice", "restaurant:r-1#staff@user:bob"],
  );

  const stats = await reconciler.reconcile();

  assert.equal(stats.checked, 2);
  assert.equal(stats.missingRepaired + stats.orphansPending, 0);
  assert.deepEqual(client.writes, []);
  assert.equal(
    client.reads[0]?.consistency?.requirement.oneofKind,
    "fullyConsistent",
  );
});

test("orphan and stale relationships are deleted only after two passes grace apart", async () => {
  const { client, clock, reconciler } = setup(
    [{ id: "r-1", ownerId: "alice", staff: [] }],
    [
      "restaurant:r-1#owner@user:alice",
      "restaurant:gone#owner@user:alice",
      "restaurant:r-1#owner@user:mallory",
      "restaurant:r-1#staff@user:former",
    ],
  );

  const first = await reconciler.reconcile();
  assert.equal(first.orphansPending, 3);
  assert.equal(first.orphansDeleted, 0);

  clock.advance(GRACE - 1);
  const early = await reconciler.reconcile();
  assert.equal(early.orphansPending, 3);
  assert.deepEqual(client.writes, []);

  clock.advance(1);
  const late = await reconciler.reconcile();
  assert.equal(late.orphansDeleted, 3);
  assert.deepEqual([...client.tuples], ["restaurant:r-1#owner@user:alice"]);
  assert.equal(reconciler.pendingCandidates, 0);
});

test("a candidate that became consistent is forgotten", async () => {
  const { store, client, clock, reconciler } = setup(
    [],
    ["restaurant:r-2#owner@user:alice"],
  );
  await reconciler.reconcile();
  assert.equal(reconciler.pendingCandidates, 1);

  // The restaurant committed after its owner relationship was written.
  store.save("r-2", 0, {
    id: "r-2",
    name: "Pho",
    ownerId: "alice",
    isOpen: false,
  });
  clock.advance(GRACE);
  const stats = await reconciler.reconcile();

  assert.equal(stats.orphansDeleted, 0);
  assert.equal(reconciler.pendingCandidates, 0);
  assert.ok(client.has("restaurant:r-2#owner@user:alice"));
});

test("relationships are read page by page", async () => {
  const restaurants = ["r-1", "r-2", "r-3", "r-4", "r-5"].map((id) => ({
    id,
    ownerId: "alice",
  }));
  const { client, reconciler } = setup(
    restaurants,
    restaurants.map(({ id }) => `restaurant:${id}#owner@user:alice`),
    { pageSize: 2 },
  );

  const stats = await reconciler.reconcile();

  assert.equal(stats.checked, 5);
  assert.equal(stats.missingRepaired, 0);
  const ownerReads = client.reads.filter(
    (read) => read.relationshipFilter?.optionalRelation === "owner",
  );
  assert.equal(ownerReads.length, 3);
  assert.equal(ownerReads[0]?.optionalLimit, 2);
});

test("ids SpiceDB cannot store are counted as unrepairable", async () => {
  const { client, reconciler } = setup(
    [
      { id: "r.1", ownerId: "alice" },
      { id: "r-2", ownerId: "bad id" },
    ],
    [],
  );

  const stats = await reconciler.reconcile();

  assert.equal(stats.unrepairable, 2);
  assert.equal(stats.missingRepaired, 0);
  assert.deepEqual(client.writes, []);
});

test("a resource id prefix scopes both sides of the reconciliation", async () => {
  const { client, clock, reconciler } = setup(
    [
      { id: "mine-1", ownerId: "alice" },
      { id: "other-1", ownerId: "alice" },
    ],
    ["restaurant:foreign#owner@user:x", "restaurant:mine-orphan#owner@user:x"],
    { resourceIdPrefix: "mine-" },
  );

  await reconciler.reconcile();
  clock.advance(GRACE);
  await reconciler.reconcile();

  assert.deepEqual(client.writes, [
    "touch restaurant:mine-1#owner@user:alice",
    "delete restaurant:mine-orphan#owner@user:x",
  ]);
  assert.ok(client.has("restaurant:foreign#owner@user:x"));
});

test("a SpiceDB error ends the pass with an error", async () => {
  const { client, reconciler } = setup([{ id: "r-1" }], []);
  client.failReads = new Error("unavailable");

  await assert.rejects(reconciler.reconcile(), /read restaurant#owner/);
});

test("an aborted signal stops the pass", async () => {
  const { client, reconciler } = setup([{ id: "r-1" }], []);

  await assert.rejects(reconciler.reconcile(AbortSignal.abort()));
  assert.deepEqual(client.writes, []);
});

test("a consistent restaurant keeps its revision; an orphan delete stores none", async () => {
  const { store, reconciler, clock } = setup(
    [{ id: "r-1", ownerId: "alice", authorizationRevision: "zed-old" }],
    ["restaurant:r-1#owner@user:alice", "restaurant:r-gone#owner@user:eve"],
  );

  await reconciler.reconcile();
  clock.advance(GRACE);
  const stats = await reconciler.reconcile();

  assert.equal(stats.orphansDeleted, 1);
  assert.equal(store.get("r-1")?.document.authorizationRevision, "zed-old");
  assert.equal(store.get("r-1")?.version, 1);
  assert.equal(store.get("r-gone"), undefined);
});
