import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";

import { v1 } from "@authzed/authzed-node";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";

import {
  AssignStaffRequestSchema,
  RegisterRestaurantRequestSchema,
  RevokeStaffRequestSchema,
} from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import {
  AUTHORIZATION_REVISION_HEADER,
  createRestaurantsService,
} from "../infrastructure/grpc/service.ts";
import { SpiceDbAccessControl } from "../infrastructure/spicedb/access-control.ts";
import {
  createSpiceDbClient,
  type SpiceDbClient,
} from "../infrastructure/spicedb/client.ts";
import { SpiceDbRelationshipWriter } from "../infrastructure/spicedb/relationships.ts";
import type { Subject } from "../pkg/application/subject.ts";
import { FixedClock } from "../pkg/infrastructure/memory/clock.ts";
import { SequentialIdGenerator } from "../pkg/infrastructure/memory/id-generator.ts";
import { AssignStaffHandler } from "../slices/restaurant/applications/assign-staff/assign-staff.ts";
import { GetRestaurantHandler } from "../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import { RegisterRestaurantHandler } from "../slices/restaurant/applications/register-restaurant/register-restaurant.ts";
import { RevokeStaffHandler } from "../slices/restaurant/applications/revoke-staff/revoke-staff.ts";
import {
  createRestaurantStore,
  InMemoryRestaurantCatalog,
  InMemoryRestaurantRepository,
} from "../slices/restaurant/infrastructure/memory/restaurants.ts";
import { PICKUP_ADDRESS, startOfDay } from "./support/restaurants.ts";

// FR-446 through the gRPC service composed like cmd/restaurants/main.ts:
// SpiceDbAccessControl and SpiceDbRelationshipWriter over one client and an
// in-memory repository. Runs against the shared SpiceDB of `make spicedb-dev`
// (SPICEDB_ENDPOINT=localhost:50051, key `devkey`, plaintext). Ids carry a
// unique dot-free prefix; created relationships are deleted afterwards.
const endpoint = process.env.SPICEDB_ENDPOINT?.trim() ?? "";
const skip = endpoint === "" ? "SPICEDB_ENDPOINT is not set" : false;

const operatorGrant = (
  operation: v1.RelationshipUpdate_Operation,
  userId: string,
) =>
  v1.WriteRelationshipsRequest.create({
    updates: [
      v1.RelationshipUpdate.create({
        operation,
        relationship: v1.Relationship.create({
          resource: v1.ObjectReference.create({
            objectType: "platform",
            objectId: "nomnom",
          }),
          relation: "operator",
          subject: v1.SubjectReference.create({
            object: v1.ObjectReference.create({
              objectType: "user",
              objectId: userId,
            }),
          }),
        }),
      }),
    ],
  });

// Request context; `authorization-revision` carries the ZedToken of the
// operator grant written by `before`.
const requestHeader = new Headers();
const context = { requestHeader } as never;

const rejectsWith = (code: Code) => (error: unknown) =>
  error instanceof ConnectError && error.code === code;

describe("FR-446 admin RPCs against SpiceDB", { skip }, () => {
  const run = `it-admin-${randomUUID()}-`;
  const operator: Subject = { type: "user", id: `${run}operator` };
  const owner: Subject = { type: "user", id: `${run}owner` };
  const stranger: Subject = { type: "user", id: `${run}stranger` };
  const cook = `${run}cook`;
  let client: SpiceDbClient;

  before(async () => {
    client = createSpiceDbClient({
      enabled: true,
      endpoint,
      presharedKey: "devkey",
      insecure: true,
      timeoutMs: 5000,
    });
    const granted = await client.writeRelationships(
      operatorGrant(v1.RelationshipUpdate_Operation.TOUCH, operator.id),
    );
    requestHeader.set(
      AUTHORIZATION_REVISION_HEADER,
      granted.writtenAt?.token ?? "",
    );
  });

  after(async () => {
    try {
      await client.writeRelationships(
        operatorGrant(v1.RelationshipUpdate_Operation.DELETE, operator.id),
      );
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
      }
    } finally {
      client.close();
    }
  });

  /** Service with a switchable caller; restaurant ids start with `<run><scope>-`. */
  const world = (scope: string) => {
    // The adapter with its own consistency, as in production: checks of a
    // restaurant are at_least_as_fresh with its stored revision (FR-447).
    const writer = new SpiceDbRelationshipWriter({ client, timeoutMs: 5000 });
    const store = createRestaurantStore();
    const restaurants = new InMemoryRestaurantRepository(store);
    const accessControl = new SpiceDbAccessControl({
      client,
      revisions: restaurants,
      timeoutMs: 5000,
    });
    /** Check through the adapter right after the previous command. */
    const can = (
      permission: "manage" | "operate_kitchen",
      restaurantId: string,
      userId: string,
    ): Promise<boolean> => {
      const subject: Subject = { type: "user", id: userId };
      return permission === "manage"
        ? accessControl.canManageRestaurant(subject, restaurantId)
        : accessControl.canOperateKitchen(subject, restaurantId);
    };
    const staff = { accessControl, staffRelationships: writer, restaurants };
    let caller: Subject = operator;
    const unused = {
      execute: async (): Promise<never> => assert.fail("unexpected call"),
    };
    const service = createRestaurantsService({
      getRestaurant: new GetRestaurantHandler({
        reader: new InMemoryRestaurantCatalog(store),
      }),
      getMenu: unused,
      listRestaurants: unused,
      receiveOrderRequest: unused,
      cancelRestaurantOrder: unused,
      acceptRestaurantOrder: unused,
      rejectRestaurantOrder: unused,
      startPreparation: unused,
      markRestaurantOrderAssembled: unused,
      markRestaurantOrderReady: unused,
      reportPreparationDelay: unused,
      reportPreparationFailure: unused,
      getKitchenQueue: unused,
      listIncomingOrders: unused,
      registerRestaurant: new RegisterRestaurantHandler({
        accessControl,
        ownerRelationships: writer,
        restaurants,
        ids: new SequentialIdGenerator(`${run}${scope}-restaurant`),
        clock: new FixedClock(startOfDay),
      }),
      assignStaff: new AssignStaffHandler(staff),
      revokeStaff: new RevokeStaffHandler(staff),
      openRestaurant: unused,
      closeRestaurant: unused,
      createMenu: unused,
      addMenuItem: unused,
      updateMenuItem: unused,
      setMenuItemAvailability: unused,
      subjects: { resolve: async () => caller },
    });
    const as = (subject: Subject) => {
      caller = subject;
      return service;
    };
    // The caller passes the ZedToken of the operator grant, so the platform
    // check of RegisterRestaurant sees it at once without retries.
    const register = () =>
      as(operator).registerRestaurant(
        create(RegisterRestaurantRequestSchema, {
          name: "Pho",
          ownerId: owner.id,
          address: PICKUP_ADDRESS,
        }),
        context,
      );
    return { as, register, restaurants, can };
  };

  test("AC-479 operator registers a restaurant and the owner gets manage", async () => {
    const { register, can } = world("register");

    const response = await register();

    const restaurantId = response.restaurant?.restaurantId ?? "";
    assert.ok(restaurantId.startsWith(run));
    assert.equal(response.restaurant?.address, PICKUP_ADDRESS);
    assert.equal(response.version, 1n);
    assert.equal(await can("manage", restaurantId, owner.id), true);
    assert.equal(await can("manage", restaurantId, stranger.id), false);
  });

  test("AC-480 AC-482 owner assigns and revokes staff, visible immediately; repeats are changed=false", async () => {
    const { as, register, can } = world("staff");
    const restaurantId = (await register()).restaurant?.restaurantId ?? "";
    const request = { restaurantId, userId: cook };

    const assigned = await as(owner).assignStaff(
      create(AssignStaffRequestSchema, request),
      context,
    );
    assert.equal(assigned.changed, true);
    assert.deepEqual(assigned.staff, [cook]);
    assert.equal(await can("operate_kitchen", restaurantId, cook), true);
    const assignedAgain = await as(owner).assignStaff(
      create(AssignStaffRequestSchema, request),
      context,
    );
    assert.equal(assignedAgain.changed, false);

    const revoked = await as(owner).revokeStaff(
      create(RevokeStaffRequestSchema, request),
      context,
    );
    assert.equal(revoked.changed, true);
    assert.deepEqual(revoked.staff, []);
    assert.equal(await can("operate_kitchen", restaurantId, cook), false);
    const revokedAgain = await as(owner).revokeStaff(
      create(RevokeStaffRequestSchema, request),
      context,
    );
    assert.equal(revokedAgain.changed, false);
  });

  test("AC-481 a user without manage gets PERMISSION_DENIED and staff is unchanged", async () => {
    const { as, register, restaurants, can } = world("denied");
    const restaurantId = (await register()).restaurant?.restaurantId ?? "";

    await assert.rejects(
      async () =>
        as(stranger).assignStaff(
          create(AssignStaffRequestSchema, { restaurantId, userId: cook }),
          context,
        ),
      rejectsWith(Code.PermissionDenied),
    );
    await assert.rejects(
      async () =>
        as(stranger).registerRestaurant(
          create(RegisterRestaurantRequestSchema, {
            name: "Pho",
            ownerId: stranger.id,
            address: PICKUP_ADDRESS,
          }),
          context,
        ),
      rejectsWith(Code.PermissionDenied),
    );

    assert.deepEqual((await restaurants.get(restaurantId))?.staff, []);
    assert.equal(await can("operate_kitchen", restaurantId, cook), false);
  });
});
