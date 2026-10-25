import assert from "node:assert/strict";
import test from "node:test";

import {
  AuthorizationUnavailableError,
  NotFoundError,
  PermissionDeniedError,
} from "../pkg/application/errors.ts";
import type { StaffRelationshipWriter } from "../slices/restaurant/applications/access-control.ts";
import { AssignStaffHandler } from "../slices/restaurant/applications/assign-staff/assign-staff.ts";
import { RevokeStaffHandler } from "../slices/restaurant/applications/revoke-staff/revoke-staff.ts";
import {
  InvalidRestaurantError,
  Restaurant,
} from "../slices/restaurant/domains/restaurant/restaurant.ts";
import {
  createRestaurantWorld,
  owner,
  PICKUP_ADDRESS,
  startOfDay,
  stranger,
} from "./support/restaurants.ts";

const cook = "cook";

test("AC-476 assign staff writes TOUCH restaurant#staff before saving", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho", { open: false });
  const calls: string[] = [];
  const assign = new AssignStaffHandler({
    accessControl: world.authorization,
    restaurants: world.restaurants,
    staffRelationships: {
      touchStaff: async (input) => {
        calls.push(`touch:${input.restaurantId}:${input.userId}`);
        assert.deepEqual(
          (await world.restaurants.get(restaurantId))?.staff,
          [],
        );
        return "zed-touch";
      },
      deleteStaff: async () => assert.fail("unexpected delete"),
    },
  });

  const result = await assign.execute({
    subject: owner,
    restaurantId,
    userId: ` ${cook} `,
  });

  assert.deepEqual(result, {
    restaurantId,
    staff: [cook],
    version: 2,
    changed: true,
  });
  assert.deepEqual(calls, [`touch:${restaurantId}:${cook}`]);
  const stored = await world.restaurants.get(restaurantId);
  assert.deepEqual(stored?.staff, [cook]);
  // FR-447: the ZedToken of the TOUCH is saved with the staff change.
  assert.equal(stored?.authorizationRevision, "zed-touch");
});

test("assign staff again is a no-op without a SpiceDB write", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho");
  await world.assignStaff.execute({
    subject: owner,
    restaurantId,
    userId: cook,
  });
  const version = (await world.restaurants.get(restaurantId))?.version;

  const repeat = await world.assignStaff.execute({
    subject: owner,
    restaurantId,
    userId: cook,
  });

  assert.equal(repeat.changed, false);
  assert.equal(repeat.version, version);
  // A repeat without a write keeps the stored revision.
  assert.equal(
    (await world.restaurants.get(restaurantId))?.authorizationRevision,
    world.authorization.lastRevision,
  );
  assert.deepEqual(world.authorization.staffWrites, [
    `touch:${restaurantId}:${cook}`,
  ]);
});

test("AC-476 revoke staff writes DELETE restaurant#staff before saving", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho");
  await world.assignStaff.execute({
    subject: owner,
    restaurantId,
    userId: cook,
  });
  const calls: string[] = [];
  const revoke = new RevokeStaffHandler({
    accessControl: world.authorization,
    restaurants: world.restaurants,
    staffRelationships: {
      touchStaff: async () => assert.fail("unexpected touch"),
      deleteStaff: async (input) => {
        calls.push(`delete:${input.restaurantId}:${input.userId}`);
        assert.deepEqual((await world.restaurants.get(restaurantId))?.staff, [
          cook,
        ]);
        return "zed-delete";
      },
    },
  });

  const result = await revoke.execute({
    subject: owner,
    restaurantId,
    userId: cook,
  });

  assert.equal(result.changed, true);
  assert.deepEqual(result.staff, []);
  assert.deepEqual(calls, [`delete:${restaurantId}:${cook}`]);
  const stored = await world.restaurants.get(restaurantId);
  assert.deepEqual(stored?.staff, []);
  // FR-447: the revision of a DELETE is stored too, so a revoke is seen.
  assert.equal(stored?.authorizationRevision, "zed-delete");
});

test("revoking a user who is not staff is a no-op without a SpiceDB write", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho");

  const result = await world.revokeStaff.execute({
    subject: owner,
    restaurantId,
    userId: cook,
  });

  assert.equal(result.changed, false);
  assert.deepEqual(world.authorization.staffWrites, []);
});

test("staff changes require restaurant#manage", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho");

  for (const handler of [world.assignStaff, world.revokeStaff]) {
    await assert.rejects(
      handler.execute({ subject: stranger, restaurantId, userId: cook }),
      PermissionDeniedError,
    );
  }
  assert.deepEqual(world.authorization.staffWrites, []);
  assert.deepEqual((await world.restaurants.get(restaurantId))?.staff, []);
});

test("staff changes of an unknown restaurant fail without a write", async () => {
  const world = createRestaurantWorld();
  world.authorization.grantManager(owner, "missing");

  await assert.rejects(
    world.assignStaff.execute({
      subject: owner,
      restaurantId: "missing",
      userId: cook,
    }),
    NotFoundError,
  );
  assert.deepEqual(world.authorization.staffWrites, []);
});

test("an empty staff user id is rejected without a write", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho");

  await assert.rejects(
    world.assignStaff.execute({ subject: owner, restaurantId, userId: " " }),
    InvalidRestaurantError,
  );
  assert.deepEqual(world.authorization.staffWrites, []);
});

test("a SpiceDB failure aborts the staff change before saving", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho");
  await world.assignStaff.execute({
    subject: owner,
    restaurantId,
    userId: cook,
  });
  const unavailable: StaffRelationshipWriter = {
    touchStaff: async () => {
      throw new AuthorizationUnavailableError(new Error("down"));
    },
    deleteStaff: async () => {
      throw new AuthorizationUnavailableError(new Error("down"));
    },
  };
  const dependencies = {
    accessControl: world.authorization,
    restaurants: world.restaurants,
    staffRelationships: unavailable,
  };
  const version = (await world.restaurants.get(restaurantId))?.version;

  await assert.rejects(
    new AssignStaffHandler(dependencies).execute({
      subject: owner,
      restaurantId,
      userId: "waiter",
    }),
    AuthorizationUnavailableError,
  );
  await assert.rejects(
    new RevokeStaffHandler(dependencies).execute({
      subject: owner,
      restaurantId,
      userId: cook,
    }),
    AuthorizationUnavailableError,
  );

  const stored = await world.restaurants.get(restaurantId);
  assert.deepEqual(stored?.staff, [cook]);
  assert.equal(stored?.version, version);
});

test("staff is part of the snapshot; legacy snapshots restore without staff", () => {
  const restaurant = Restaurant.register({
    id: "r-1",
    name: "Pho",
    ownerId: owner.id,
    address: PICKUP_ADDRESS,
    registeredAt: startOfDay,
  });
  assert.equal(restaurant.assignStaff("b"), true);
  assert.equal(restaurant.assignStaff("a"), true);
  assert.equal(restaurant.assignStaff("a"), false);
  assert.deepEqual(restaurant.toSnapshot().staff, ["a", "b"]);
  assert.deepEqual(Restaurant.restore(restaurant.toSnapshot(), 1).staff, [
    "a",
    "b",
  ]);
  assert.equal(restaurant.revokeStaff("a"), true);
  assert.equal(restaurant.revokeStaff("a"), false);
  assert.deepEqual(
    restaurant.pullEvents().map((event) => event.type),
    ["RestaurantRegistered"],
  );

  const legacy = Restaurant.restore(
    { id: "r-0", name: "Old", ownerId: owner.id, isOpen: false },
    3,
  );
  assert.deepEqual(legacy.staff, []);
  assert.deepEqual(legacy.toSnapshot().staff, []);
});
