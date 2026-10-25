import assert from "node:assert/strict";
import test from "node:test";
import { SpiceDbRelationshipWriter } from "../infrastructure/spicedb/relationships.ts";
import { AssignStaffHandler } from "../slices/restaurant/applications/assign-staff/assign-staff.ts";
import { RevokeStaffHandler } from "../slices/restaurant/applications/revoke-staff/revoke-staff.ts";
import type {
  Restaurant,
  StoredRestaurantSnapshot,
} from "../slices/restaurant/domains/restaurant/restaurant.ts";
import {
  createRestaurantCollection,
  MongoRestaurantMemberships,
  MongoRestaurantRepository,
  RESTAURANT_SCHEMA_VERSION,
} from "../slices/restaurant/infrastructure/mongodb/restaurants.ts";
import { RelationshipReconciler } from "../slices/restaurant/infrastructure/reconcile/reconcile.ts";
import { createTestDatabase, noKitchenChanges } from "./support/mongodb.ts";
import { FakeRelationshipsClient } from "./support/relationships.ts";
import { FixedClock, owner } from "./support/restaurants.ts";

const GRACE = 10 * 60_000;

type RestaurantStore = ReturnType<typeof createRestaurantCollection>;

/** Stores a restaurant document as the reconciler finds it in MongoDB. */
const seed = (
  store: RestaurantStore,
  { id, ...restaurant }: Partial<StoredRestaurantSnapshot> & { id: string },
) =>
  store.save(id, 0, {
    schemaVersion: RESTAURANT_SCHEMA_VERSION,
    name: "Pho",
    ownerId: "owner",
    isOpen: false,
    ...restaurant,
    staff: restaurant.staff === undefined ? undefined : [...restaurant.staff],
  });

const setup = async (
  restaurants: Array<Partial<StoredRestaurantSnapshot> & { id: string }>,
  tuples: string[],
  options: { pageSize?: number; resourceIdPrefix?: string } = {},
) => {
  const { db, unitOfWork } = await createTestDatabase();
  const store = createRestaurantCollection(db, unitOfWork);
  for (const restaurant of restaurants) {
    await seed(store, restaurant);
  }
  const client = new FakeRelationshipsClient(...tuples);
  const clock = new FixedClock(new Date(Date.UTC(2026, 9, 25)));
  const logs: string[] = [];
  const reconciler = new RelationshipReconciler({
    client,
    restaurants: new MongoRestaurantMemberships(store),
    graceMs: GRACE,
    pageSize: options.pageSize ?? 500,
    timeoutMs: 1000,
    clock,
    resourceIdPrefix: options.resourceIdPrefix,
    log: (message) => logs.push(message),
  });
  return { store, client, clock, reconciler, logs };
};

test("AC-478 AC-701 restores missing owner and staff relationships after two passes grace apart", async () => {
  const { client, clock, reconciler, logs, store } = await setup(
    [{ id: "r-1", ownerId: "alice", staff: ["bob"] }],
    [],
  );

  const first = await reconciler.reconcile();
  assert.equal(first.missingPending, 2);
  assert.equal(first.missingRepaired, 0);
  assert.match(logs.at(-1) ?? "", /missing_pending=2 missing_repaired=0/);
  clock.advance(GRACE - 1);
  const early = await reconciler.reconcile();
  assert.equal(early.missingPending, 2);
  assert.deepEqual(client.writes, []);
  assert.equal(reconciler.pendingMissing, 2);

  clock.advance(1);
  const stats = await reconciler.reconcile();

  assert.deepEqual(stats, {
    checked: 0,
    orphansPending: 0,
    orphansDeleted: 0,
    missingPending: 0,
    missingRepaired: 2,
    unrepairable: 0,
  });
  assert.equal(reconciler.pendingMissing, 0);
  assert.ok(client.has("restaurant:r-1#owner@user:alice"));
  assert.ok(client.has("restaurant:r-1#staff@user:bob"));
  // FR-447: the revision of the last repairing TOUCH is stored.
  const stored = await store.findOne({ _id: "r-1" });
  assert.equal(stored?.authorizationRevision, "zed-2");
  assert.equal(stored?.version, 3);
  assert.match(
    logs.at(-1) ?? "",
    /checked=0 orphans_pending=0 orphans_deleted=0 missing_pending=0 missing_repaired=2 unrepairable=0/,
  );
});

test("consistent relationships are checked and left alone", async () => {
  const { client, reconciler } = await setup(
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
  const { client, clock, reconciler } = await setup(
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
  const { store, client, clock, reconciler } = await setup(
    [],
    ["restaurant:r-2#owner@user:alice"],
  );
  await reconciler.reconcile();
  assert.equal(reconciler.pendingCandidates, 1);

  // The restaurant committed after its owner relationship was written.
  await seed(store, { id: "r-2", ownerId: "alice" });
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
  const { client, reconciler } = await setup(
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
  const { client, reconciler } = await setup(
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
  const { client, clock, reconciler } = await setup(
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
    "delete restaurant:mine-orphan#owner@user:x",
    "touch restaurant:mine-1#owner@user:alice",
  ]);
  assert.ok(client.has("restaurant:foreign#owner@user:x"));
});

test("a SpiceDB error ends the pass with an error", async () => {
  const { client, reconciler } = await setup([{ id: "r-1" }], []);
  client.failReads = new Error("unavailable");

  await assert.rejects(reconciler.reconcile(), /read restaurant#owner/);
});

test("an aborted signal stops the pass", async () => {
  const { client, reconciler } = await setup([{ id: "r-1" }], []);

  await assert.rejects(reconciler.reconcile(AbortSignal.abort()));
  assert.deepEqual(client.writes, []);
});

test("a consistent restaurant keeps its revision; an orphan delete stores none", async () => {
  const { store, reconciler, clock } = await setup(
    [{ id: "r-1", ownerId: "alice", authorizationRevision: "zed-old" }],
    ["restaurant:r-1#owner@user:alice", "restaurant:r-gone#owner@user:eve"],
  );

  await reconciler.reconcile();
  clock.advance(GRACE);
  const stats = await reconciler.reconcile();

  assert.equal(stats.orphansDeleted, 1);
  assert.equal(
    (await store.findOne({ _id: "r-1" }))?.authorizationRevision,
    "zed-old",
  );
  assert.equal((await store.findOne({ _id: "r-1" }))?.version, 1);
  assert.equal(await store.findOne({ _id: "r-gone" }), undefined);
});

/** Repository whose save waits for `commit()`: the use case is "in flight". */
class GatedRestaurantRepository extends MongoRestaurantRepository {
  readonly saving: Promise<void>;
  #saving: () => void = () => {};
  #commit: () => void = () => {};
  readonly #committed: Promise<void>;

  constructor(store: RestaurantStore) {
    super(store, noKitchenChanges);
    this.saving = new Promise((resolve) => {
      this.#saving = resolve;
    });
    this.#committed = new Promise((resolve) => {
      this.#commit = resolve;
    });
  }

  commit(): void {
    this.#commit();
  }

  override async save(restaurant: Restaurant): Promise<number> {
    this.#saving();
    await this.#committed;
    return super.save(restaurant);
  }
}

test("AC-700 a revoke in flight is not restored and its candidate is forgotten after the save", async () => {
  const { store, client, clock, reconciler } = await setup(
    [{ id: "r-1", ownerId: owner.id, staff: ["bob"] }],
    [`restaurant:r-1#owner@user:${owner.id}`, "restaurant:r-1#staff@user:bob"],
  );
  const restaurants = new GatedRestaurantRepository(store);
  const revoke = new RevokeStaffHandler({
    accessControl: {
      canRegisterRestaurant: async () => false,
      canManageRestaurant: async () => true,
    },
    staffRelationships: new SpiceDbRelationshipWriter({
      client,
      timeoutMs: 1000,
    }),
    restaurants,
  });

  // Revoke Staff DELETEs in SpiceDB and waits before its save.
  const revoking = revoke.execute({
    subject: owner,
    restaurantId: "r-1",
    userId: "bob",
  });
  await restaurants.saving;
  assert.equal(client.has("restaurant:r-1#staff@user:bob"), false);
  assert.deepEqual((await store.findOne({ _id: "r-1" }))?.staff, ["bob"]);

  // The pass reads the graph after the DELETE and the restaurant before the save.
  const during = await reconciler.reconcile();
  assert.equal(client.has("restaurant:r-1#staff@user:bob"), false);
  assert.equal(during.missingRepaired, 0);
  assert.equal(during.missingPending, 1);

  restaurants.commit();
  assert.equal((await revoking).changed, true);
  clock.advance(GRACE);
  const after = await reconciler.reconcile();

  assert.equal(after.missingPending, 0);
  assert.equal(after.missingRepaired, 0);
  assert.equal(reconciler.pendingMissing, 0);
  assert.deepEqual(client.writes, ["delete restaurant:r-1#staff@user:bob"]);
  assert.equal(client.has("restaurant:r-1#staff@user:bob"), false);
});

test("a missing candidate whose relationship came back is forgotten", async () => {
  const { client, clock, reconciler } = await setup(
    [{ id: "r-1", ownerId: "alice" }],
    [],
  );
  await reconciler.reconcile();
  assert.equal(reconciler.pendingMissing, 1);

  client.tuples.add("restaurant:r-1#owner@user:alice");
  await reconciler.reconcile();
  assert.equal(reconciler.pendingMissing, 0);

  // Lost again: the grace starts over.
  client.tuples.delete("restaurant:r-1#owner@user:alice");
  clock.advance(GRACE);
  const stats = await reconciler.reconcile();
  assert.equal(stats.missingPending, 1);
  assert.deepEqual(client.writes, []);
});

test("AC-710 a re-assign and a second revoke between passes restart the grace", async () => {
  const { store, client, clock, reconciler } = await setup(
    [{ id: "r-1", ownerId: owner.id, staff: ["bob"] }],
    [`restaurant:r-1#owner@user:${owner.id}`, "restaurant:r-1#staff@user:bob"],
  );
  const writer = new SpiceDbRelationshipWriter({ client, timeoutMs: 1000 });
  const accessControl = {
    canRegisterRestaurant: async () => false,
    canManageRestaurant: async () => true,
  };
  const revokeInFlight = () => {
    const restaurants = new GatedRestaurantRepository(store);
    const revoking = new RevokeStaffHandler({
      accessControl,
      staffRelationships: writer,
      restaurants,
    }).execute({ subject: owner, restaurantId: "r-1", userId: "bob" });
    return { restaurants, revoking };
  };

  // Pass 1 sees the first revoke in flight: DELETEd, not yet saved.
  const first = revokeInFlight();
  await first.restaurants.saving;
  const version1 = (await store.findOne({ _id: "r-1" }))?.version;
  assert.equal((await reconciler.reconcile()).missingPending, 1);

  // Between the passes: the revoke is saved, bob is re-assigned, and a second
  // revoke DELETEs again and waits before its save.
  first.restaurants.commit();
  await first.revoking;
  await new AssignStaffHandler({
    accessControl,
    staffRelationships: writer,
    restaurants: new MongoRestaurantRepository(store, noKitchenChanges),
  }).execute({ subject: owner, restaurantId: "r-1", userId: "bob" });
  const second = revokeInFlight();
  await second.restaurants.saving;
  assert.notEqual((await store.findOne({ _id: "r-1" }))?.version, version1);
  assert.deepEqual((await store.findOne({ _id: "r-1" }))?.staff, ["bob"]);

  // Pass 2, grace later, sees bob missing again but on another version.
  clock.advance(GRACE);
  const during = await reconciler.reconcile();
  assert.equal(during.missingRepaired, 0);
  assert.equal(during.missingPending, 1, "the grace starts over");
  assert.equal(client.has("restaurant:r-1#staff@user:bob"), false);

  second.restaurants.commit();
  await second.revoking;
  clock.advance(GRACE);
  const after = await reconciler.reconcile();
  assert.equal(after.missingRepaired, 0);
  assert.equal(reconciler.pendingMissing, 0);
  assert.equal(client.has("restaurant:r-1#staff@user:bob"), false);
  // The only TOUCH is Assign Staff's own; the reconciler restored nothing.
  assert.deepEqual(client.writes, [
    "delete restaurant:r-1#staff@user:bob",
    "touch restaurant:r-1#staff@user:bob",
    "delete restaurant:r-1#staff@user:bob",
  ]);
});

test("a missing relationship on an unchanged version is restored after the grace", async () => {
  const { store, client, clock, reconciler } = await setup(
    [{ id: "r-1", ownerId: "alice", staff: ["bob"] }],
    ["restaurant:r-1#owner@user:alice"],
  );
  await reconciler.reconcile();
  // An unrelated save between the passes restarts the grace.
  const stored = await store.findOne({ _id: "r-1" });
  assert.ok(stored);
  const { _id, version, ...document } = stored;
  await store.save("r-1", version, { ...document, name: "Pho 2" });
  clock.advance(GRACE);
  assert.equal((await reconciler.reconcile()).missingRepaired, 0);

  clock.advance(GRACE);
  assert.equal((await reconciler.reconcile()).missingRepaired, 1);
  assert.ok(client.has("restaurant:r-1#staff@user:bob"));
});
