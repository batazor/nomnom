import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";
import { v1 } from "@authzed/authzed-node";
import { migrateRestaurantsDatabase } from "../infrastructure/mongodb/collections.ts";
import {
  createSpiceDbClient,
  type SpiceDbClient,
} from "../infrastructure/spicedb/client.ts";
import { SpiceDbRelationshipWriter } from "../infrastructure/spicedb/relationships.ts";
import {
  connectMongoDb,
  type MongoDbConnection,
} from "../pkg/infrastructure/mongodb/client.ts";
import { MongoUnitOfWork } from "../pkg/infrastructure/mongodb/unit-of-work.ts";
import { RevokeStaffHandler } from "../slices/restaurant/applications/revoke-staff/revoke-staff.ts";
import { Restaurant } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import {
  createRestaurantCollection,
  MongoRestaurantMemberships,
  MongoRestaurantRepository,
} from "../slices/restaurant/infrastructure/mongodb/restaurants.ts";
import { RelationshipReconciler } from "../slices/restaurant/infrastructure/reconcile/reconcile.ts";
import { spiceDbEndpoint } from "./support/global-containers.ts";
import { mongoUri, noKitchenChanges } from "./support/mongodb.ts";
import { FixedClock, owner, startOfDay } from "./support/restaurants.ts";

// Revoke Staff in flight against MongoDB and SpiceDB (AC-700): needs the MongoDB
// testcontainer of the global setup and the
// SpiceDB testcontainer of the global setup (key `devkey`,
// plaintext). Ids start with a unique prefix the reconciler is scoped to.
const endpoint = spiceDbEndpoint();
const GRACE = 60_000;

/** MongoDB repository whose save waits for `commit()`. */
class GatedRestaurantRepository extends MongoRestaurantRepository {
  saving: Promise<void> = Promise.resolve();
  #saving: () => void = () => {};
  #commit: () => void = () => {};
  #committed: Promise<void> = Promise.resolve();

  gate(): void {
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

describe("reconciliation against MongoDB and SpiceDB", () => {
  const run = `it-${randomUUID()}-`;
  let mongo: MongoDbConnection;
  let spicedb: SpiceDbClient;

  before(async () => {
    mongo = await connectMongoDb({
      enabled: true,
      uri: mongoUri(),
      database: `restaurants_test_${randomUUID().slice(0, 8)}`,
      timeoutMs: 5000,
    });
    await migrateRestaurantsDatabase(mongo.db);
    spicedb = createSpiceDbClient({
      enabled: true,
      endpoint,
      presharedKey: "devkey",
      insecure: true,
      timeoutMs: 5000,
    });
  });

  after(async () => {
    if (spicedb !== undefined) {
      const raw = v1.NewClient(
        "devkey",
        endpoint,
        v1.ClientSecurity.INSECURE_PLAINTEXT_CREDENTIALS,
      );
      try {
        for (const relation of ["owner", "staff"]) {
          await raw.promises.deleteRelationships(
            v1.DeleteRelationshipsRequest.create({
              relationshipFilter: v1.RelationshipFilter.create({
                resourceType: "restaurant",
                optionalResourceIdPrefix: run,
                optionalRelation: relation,
              }),
            }),
          );
        }
      } finally {
        raw.close();
        spicedb.close();
      }
    }
    if (mongo !== undefined) {
      await mongo.db.dropDatabase();
      await mongo.close();
    }
  });

  const isStaff = async (restaurantId: string, userId: string) => {
    const responses = await spicedb.readRelationships(
      v1.ReadRelationshipsRequest.create({
        consistency: v1.Consistency.create({
          requirement: { oneofKind: "fullyConsistent", fullyConsistent: true },
        }),
        relationshipFilter: v1.RelationshipFilter.create({
          resourceType: "restaurant",
          optionalResourceId: restaurantId,
          optionalRelation: "staff",
          optionalSubjectFilter: v1.SubjectFilter.create({
            subjectType: "user",
            optionalSubjectId: userId,
          }),
        }),
      }),
    );
    return responses.length > 0;
  };

  test("AC-700 a revoke in flight is not restored by a pass between its DELETE and its save", async () => {
    const collection = createRestaurantCollection(
      mongo.db,
      new MongoUnitOfWork(mongo.client),
    );
    const restaurants = new GatedRestaurantRepository(
      collection,
      noKitchenChanges,
    );
    const writer = new SpiceDbRelationshipWriter({
      client: spicedb,
      timeoutMs: 5000,
    });
    const restaurantId = `${run}revoke`;
    const cook = `${run}cook`;
    const restaurant = Restaurant.register({
      id: restaurantId,
      name: "Pho",
      ownerId: owner.id,
      address: "1 Pho Street",
      registeredAt: startOfDay,
    });
    restaurant.assignStaff(cook);
    await restaurants.save(restaurant);
    await writer.touchOwner({ restaurantId, ownerId: owner.id });
    await writer.touchStaff({ restaurantId, userId: cook });

    const clock = new FixedClock(startOfDay);
    const reconciler = new RelationshipReconciler({
      client: spicedb,
      restaurants: new MongoRestaurantMemberships(collection),
      graceMs: GRACE,
      pageSize: 2,
      timeoutMs: 5000,
      clock,
      resourceIdPrefix: run,
      log: () => {},
    });
    const revoke = new RevokeStaffHandler({
      accessControl: {
        canRegisterRestaurant: async () => false,
        canManageRestaurant: async () => true,
      },
      staffRelationships: writer,
      restaurants,
    });

    restaurants.gate();
    const revoking = revoke.execute({
      subject: owner,
      restaurantId,
      userId: cook,
    });
    await restaurants.saving;
    assert.equal(await isStaff(restaurantId, cook), false);

    // Graph read after the DELETE, restaurant read before the save.
    const during = await reconciler.reconcile();
    assert.equal(await isStaff(restaurantId, cook), false);
    assert.equal(during.missingRepaired, 0);
    assert.equal(during.missingPending, 1);

    restaurants.commit();
    assert.equal((await revoking).changed, true);
    clock.advance(GRACE);
    const settled = await reconciler.reconcile();

    assert.equal(settled.checked, 1);
    assert.equal(settled.missingPending, 0);
    assert.equal(settled.missingRepaired, 0);
    assert.equal(reconciler.pendingMissing, 0);
    assert.equal(await isStaff(restaurantId, cook), false);
    assert.deepEqual((await restaurants.get(restaurantId))?.staff, []);
  });
});
