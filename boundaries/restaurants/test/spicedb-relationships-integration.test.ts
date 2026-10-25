import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, describe, test } from "node:test";

import { v1 } from "@authzed/authzed-node";

import {
  checkPermissionRequest,
  createSpiceDbClient,
  SpiceDbAccessControl,
  type SpiceDbClient,
} from "../infrastructure/spicedb/access-control.ts";
import { SpiceDbRelationshipWriter } from "../infrastructure/spicedb/relationships.ts";
import type { Subject } from "../pkg/application/subject.ts";
import { FixedClock } from "../pkg/infrastructure/memory/clock.ts";
import { SequentialIdGenerator } from "../pkg/infrastructure/memory/id-generator.ts";
import type { RestaurantAccessControl } from "../slices/restaurant/applications/access-control.ts";
import { AssignStaffHandler } from "../slices/restaurant/applications/assign-staff/assign-staff.ts";
import { RegisterRestaurantHandler } from "../slices/restaurant/applications/register-restaurant/register-restaurant.ts";
import { RevokeStaffHandler } from "../slices/restaurant/applications/revoke-staff/revoke-staff.ts";
import {
  createRestaurantStore,
  InMemoryRestaurantMemberships,
  InMemoryRestaurantRepository,
} from "../slices/restaurant/infrastructure/memory/restaurants.ts";
import { RelationshipReconciler } from "../slices/restaurant/infrastructure/reconcile/reconcile.ts";
import { PICKUP_ADDRESS, startOfDay } from "./support/restaurants.ts";

// Runs against the shared SpiceDB of `make spicedb-dev`
// (SPICEDB_ENDPOINT=localhost:50051, key `devkey`, plaintext). Every id of a
// run starts with a unique prefix without dots; the reconciler is scoped to it
// so other tests' relationships are never touched.
const endpoint = process.env.SPICEDB_ENDPOINT?.trim() ?? "";
const skip = endpoint === "" ? "SPICEDB_ENDPOINT is not set" : false;
const GRACE = 60_000;

describe("SpiceDB restaurant relationships", { skip }, () => {
  const run = `it-${randomUUID()}-`;
  let client: SpiceDbClient | undefined;
  const spicedb = (): SpiceDbClient => {
    client ??= createSpiceDbClient({
      enabled: true,
      endpoint,
      presharedKey: "devkey",
      insecure: true,
      timeoutMs: 5000,
    });
    return client;
  };

  after(async () => {
    if (client === undefined) return;
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
      client.close();
    }
  });

  /**
   * Fully consistent check for relationships written outside the use cases
   * (lost owner, orphans): they have no stored revision.
   */
  const can = async (
    permission: string,
    restaurantId: string,
    userId: string,
  ): Promise<boolean> => {
    const request = checkPermissionRequest(
      "restaurant",
      restaurantId,
      permission,
      { type: "user", id: userId },
    );
    request.consistency = v1.Consistency.create({
      requirement: { oneofKind: "fullyConsistent", fullyConsistent: true },
    });
    const response = await spicedb().checkPermission(request);
    return (
      response.permissionship ===
      v1.CheckPermissionResponse_Permissionship.HAS_PERMISSION
    );
  };

  /** Restaurant ids of a world start with `<run><scope>-`. */
  const world = (scope: string) => {
    const store = createRestaurantStore();
    const restaurants = new InMemoryRestaurantRepository(store);
    const writer = new SpiceDbRelationshipWriter({
      client: spicedb(),
      timeoutMs: 5000,
    });
    // The production adapter with its own consistency: at_least_as_fresh
    // with the revision stored by the use cases (FR-447), no forced full
    // consistency.
    const adapter = new SpiceDbAccessControl({
      client: spicedb(),
      revisions: restaurants,
      timeoutMs: 5000,
    });
    // Registration is a platform grant outside this test; manage is checked
    // in SpiceDB.
    const accessControl: RestaurantAccessControl = {
      canRegisterRestaurant: async () => true,
      canManageRestaurant: (subject: Subject, restaurantId: string) =>
        adapter.canManageRestaurant(subject, restaurantId),
    };
    const register = new RegisterRestaurantHandler({
      accessControl,
      ownerRelationships: writer,
      restaurants,
      ids: new SequentialIdGenerator(`${run}${scope}-restaurant`),
      clock: new FixedClock(startOfDay),
    });
    const staff = { accessControl, staffRelationships: writer, restaurants };
    return {
      store,
      restaurants,
      adapter,
      register,
      assign: new AssignStaffHandler(staff),
      revoke: new RevokeStaffHandler(staff),
    };
  };

  const operator: Subject = { type: "user", id: `${run}operator` };
  const owner: Subject = { type: "user", id: `${run}owner` };
  const cook = `${run}cook`;

  const cookSubject: Subject = { type: "user", id: cook };

  test("FR-443 FR-447 registration gives the owner restaurant#manage immediately", async () => {
    const { register, restaurants, adapter } = world("owner");

    const { restaurantId } = await register.execute({
      subject: operator,
      name: "Pho",
      ownerId: owner.id,
      address: PICKUP_ADDRESS,
    });

    assert.ok(
      (await restaurants.get(restaurantId))?.authorizationRevision !==
        undefined,
    );
    assert.equal(await adapter.canManageRestaurant(owner, restaurantId), true);
    assert.equal(
      await adapter.canManageRestaurant(cookSubject, restaurantId),
      false,
    );
  });

  test("AC-476 AC-482 staff gets operate_kitchen right after assign and loses it right after revoke", async () => {
    const { register, assign, revoke, restaurants, adapter } = world("staff");
    const { restaurantId } = await register.execute({
      subject: operator,
      name: "Pho",
      ownerId: owner.id,
      address: PICKUP_ADDRESS,
    });
    assert.equal(
      await adapter.canOperateKitchen(cookSubject, restaurantId),
      false,
    );
    const registered = (await restaurants.get(restaurantId))
      ?.authorizationRevision;

    const assigned = await assign.execute({
      subject: owner,
      restaurantId,
      userId: cook,
    });
    assert.equal(assigned.changed, true);
    const afterAssign = (await restaurants.get(restaurantId))
      ?.authorizationRevision;
    assert.ok(afterAssign !== undefined && afterAssign !== registered);
    assert.equal(
      await adapter.canOperateKitchen(cookSubject, restaurantId),
      true,
    );
    assert.equal(
      await adapter.canManageRestaurant(cookSubject, restaurantId),
      false,
    );

    const revoked = await revoke.execute({
      subject: owner,
      restaurantId,
      userId: cook,
    });
    assert.equal(revoked.changed, true);
    assert.notEqual(
      (await restaurants.get(restaurantId))?.authorizationRevision,
      afterAssign,
    );
    assert.equal(
      await adapter.canOperateKitchen(cookSubject, restaurantId),
      false,
    );
  });

  test("AC-478 reconciler restores a lost owner and deletes an orphan after two passes grace apart", async () => {
    const { store, register, restaurants, adapter } = world("reconcile");
    const { restaurantId } = await register.execute({
      subject: operator,
      name: "Pho",
      ownerId: owner.id,
      address: PICKUP_ADDRESS,
    });
    const writer = new SpiceDbRelationshipWriter({
      client: spicedb(),
      timeoutMs: 5000,
    });
    // Lost owner relationship.
    await spicedb().writeRelationships(
      v1.WriteRelationshipsRequest.create({
        updates: [
          v1.RelationshipUpdate.create({
            operation: v1.RelationshipUpdate_Operation.DELETE,
            relationship: v1.Relationship.create({
              resource: v1.ObjectReference.create({
                objectType: "restaurant",
                objectId: restaurantId,
              }),
              relation: "owner",
              subject: v1.SubjectReference.create({
                object: v1.ObjectReference.create({
                  objectType: "user",
                  objectId: owner.id,
                }),
              }),
            }),
          }),
        ],
      }),
    );
    assert.equal(await can("manage", restaurantId, owner.id), false);
    // Orphan: a written relationship whose restaurant was never stored.
    const orphanId = `${run}reconcile-orphan`;
    const intruder = `${run}intruder`;
    await writer.touchOwner({ restaurantId: orphanId, ownerId: intruder });

    const clock = new FixedClock(startOfDay);
    const reconciler = new RelationshipReconciler({
      client: spicedb(),
      restaurants: new InMemoryRestaurantMemberships(store),
      graceMs: GRACE,
      pageSize: 2,
      timeoutMs: 5000,
      clock,
      resourceIdPrefix: `${run}reconcile-`,
      log: () => {},
    });

    const registered = (await restaurants.get(restaurantId))
      ?.authorizationRevision;
    const first = await reconciler.reconcile();
    assert.equal(first.missingPending, 1);
    assert.equal(first.missingRepaired, 0);
    assert.equal(first.orphansPending, 1);
    assert.equal(await can("manage", restaurantId, owner.id), false);
    assert.equal(await can("manage", orphanId, intruder), true);

    clock.advance(GRACE - 1);
    const early = await reconciler.reconcile();
    assert.equal(early.orphansDeleted, 0);
    assert.equal(early.missingRepaired, 0);
    assert.equal(await can("manage", orphanId, intruder), true);

    clock.advance(1);
    const late = await reconciler.reconcile();
    assert.equal(late.orphansDeleted, 1);
    assert.equal(late.missingRepaired, 1);
    // The repairing TOUCH revision is stored, so the adapter sees it at once.
    assert.notEqual(
      (await restaurants.get(restaurantId))?.authorizationRevision,
      registered,
    );
    assert.equal(await adapter.canManageRestaurant(owner, restaurantId), true);
    assert.equal(await can("manage", restaurantId, owner.id), true);
    assert.equal(await can("manage", orphanId, intruder), false);

    const settled = await reconciler.reconcile();
    assert.equal(settled.missingPending + settled.missingRepaired, 0);
  });
});
