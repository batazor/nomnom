import assert from "node:assert/strict";
import test from "node:test";

import {
  NotFoundError,
  PermissionDeniedError,
} from "../pkg/application/errors.ts";
import type { Outbox } from "../pkg/application/outbox.ts";
import { GetRestaurantHandler } from "../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import {
  InvalidPageSizeError,
  ListRestaurantsHandler,
} from "../slices/restaurant/applications/list-restaurants/list-restaurants.ts";
import { RegisterRestaurantHandler } from "../slices/restaurant/applications/register-restaurant/register-restaurant.ts";
import { UpdateRestaurantHandler } from "../slices/restaurant/applications/update-restaurant/update-restaurant.ts";
import {
  InvalidRestaurantError,
  InvalidRestaurantReason,
} from "../slices/restaurant/domains/restaurant/restaurant.ts";
import {
  createInfrastructure,
  createRestaurantWorld,
  operator,
  owner,
  PICKUP_ADDRESS,
  stranger,
} from "./support/restaurants.ts";

test("AC-202 register restaurant creates a closed restaurant after the owner relationship", async () => {
  const world = createRestaurantWorld();
  const calls: string[] = [];
  const register = new RegisterRestaurantHandler({
    accessControl: world.authorization,
    ownerRelationships: {
      touchOwner: async (input) => {
        calls.push(`touch:${input.restaurantId}`);
        assert.equal(
          await world.restaurants.get(input.restaurantId),
          undefined,
        );
        await world.authorization.touchOwner(input);
      },
    },
    restaurants: world.restaurants,
    ids: world.ids,
    clock: world.clock,
  });

  const result = await register.execute({
    subject: operator,
    name: "Pho Bar",
    ownerId: owner.id,
    address: PICKUP_ADDRESS,
  });

  assert.deepEqual(result, {
    restaurantId: "restaurant-1",
    name: "Pho Bar",
    isOpen: false,
    address: PICKUP_ADDRESS,
    version: 1,
  });
  assert.deepEqual(calls, ["touch:restaurant-1"]);
  assert.deepEqual(world.authorization.owners, [
    { restaurantId: "restaurant-1", ownerId: owner.id },
  ]);
  assert.equal((await world.restaurants.get("restaurant-1"))?.isOpen, false);
  assert.deepEqual(world.outbox.records, []);
});

test("register restaurant requires the registration permission", async () => {
  const world = createRestaurantWorld();

  await assert.rejects(
    world.register.execute({
      subject: stranger,
      name: "Pho",
      ownerId: owner.id,
      address: PICKUP_ADDRESS,
    }),
    PermissionDeniedError,
  );
  assert.deepEqual(world.store.all(), []);
  assert.deepEqual(world.authorization.owners, []);
});

test("AC-203 register restaurant rejects a blank name", async () => {
  const world = createRestaurantWorld();

  await assert.rejects(
    world.register.execute({
      subject: operator,
      name: "  ",
      ownerId: owner.id,
      address: PICKUP_ADDRESS,
    }),
    InvalidRestaurantError,
  );
  assert.deepEqual(world.store.all(), []);
});

test("FR-428 register restaurant requires a pickup address", async () => {
  const world = createRestaurantWorld();

  await assert.rejects(
    world.register.execute({
      subject: operator,
      name: "Pho",
      ownerId: owner.id,
      address: "  ",
    }),
    (error: unknown) =>
      error instanceof InvalidRestaurantError &&
      error.reason === InvalidRestaurantReason.EmptyAddress,
  );
  assert.deepEqual(world.store.all(), []);
  assert.deepEqual(world.authorization.owners, []);
});

test("AC-204 restaurant is not stored when the owner relationship write fails", async () => {
  const world = createRestaurantWorld();
  const register = new RegisterRestaurantHandler({
    accessControl: world.authorization,
    ownerRelationships: {
      touchOwner: async () => {
        throw new Error("spicedb unavailable");
      },
    },
    restaurants: world.restaurants,
    ids: world.ids,
    clock: world.clock,
  });

  await assert.rejects(
    register.execute({
      subject: operator,
      name: "Pho",
      ownerId: owner.id,
      address: PICKUP_ADDRESS,
    }),
    /spicedb unavailable/,
  );
  assert.deepEqual(world.store.all(), []);
});

test("AC-205 update restaurant renames without touching the opening", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");
  const update = new UpdateRestaurantHandler({
    accessControl: world.authorization,
    restaurants: world.restaurants,
    clock: world.clock,
  });
  const outboxBefore = world.outbox.records.length;

  const result = await update.execute({
    subject: owner,
    restaurantId,
    name: "Ramen Bar",
  });
  const unchanged = await update.execute({
    subject: owner,
    restaurantId,
    name: "Ramen Bar",
  });

  assert.deepEqual(result, {
    restaurantId,
    name: "Ramen Bar",
    isOpen: true,
    address: PICKUP_ADDRESS,
    version: 3,
    changed: true,
  });
  assert.equal(unchanged.changed, false);
  assert.equal(unchanged.version, 3);
  assert.equal(world.outbox.records.length, outboxBefore);
});

test("FR-428 update restaurant changes the pickup address", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");
  const update = new UpdateRestaurantHandler({
    accessControl: world.authorization,
    restaurants: world.restaurants,
    clock: world.clock,
  });
  const outboxBefore = world.outbox.records.length;

  const result = await update.execute({
    subject: owner,
    restaurantId,
    name: "Pho Bar",
    address: " 2 Ramen Lane ",
  });
  const unchanged = await update.execute({
    subject: owner,
    restaurantId,
    name: "Pho Bar",
    address: "2 Ramen Lane",
  });

  assert.equal(result.changed, true);
  assert.equal(result.address, "2 Ramen Lane");
  assert.equal(unchanged.changed, false);
  assert.equal(
    (await world.restaurants.get(restaurantId))?.address,
    "2 Ramen Lane",
  );
  await assert.rejects(
    update.execute({ subject: owner, restaurantId, name: "Pho", address: "" }),
    InvalidRestaurantError,
  );
  assert.equal(world.outbox.records.length, outboxBefore);
});

test("AC-206 update of an unknown restaurant is not found", async () => {
  const world = createRestaurantWorld();
  world.authorization.grantManager(owner, "missing");
  const update = new UpdateRestaurantHandler({
    accessControl: world.authorization,
    restaurants: world.restaurants,
    clock: world.clock,
  });

  await assert.rejects(
    update.execute({ subject: owner, restaurantId: "missing", name: "Pho" }),
    NotFoundError,
  );
});

test("AC-200 manager commands are denied without the manage permission", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar", { open: false });
  const update = new UpdateRestaurantHandler({
    accessControl: world.authorization,
    restaurants: world.restaurants,
    clock: world.clock,
  });

  await assert.rejects(
    update.execute({ subject: stranger, restaurantId, name: "Ramen" }),
    PermissionDeniedError,
  );
  await assert.rejects(
    world.open.execute({ subject: stranger, restaurantId }),
    PermissionDeniedError,
  );
  await assert.rejects(
    world.close.execute({ subject: stranger, restaurantId }),
    PermissionDeniedError,
  );
  const restaurant = await world.restaurants.get(restaurantId);
  assert.equal(restaurant?.name, "Pho Bar");
  assert.equal(restaurant?.isOpen, false);
  assert.deepEqual(world.outbox.records, []);
});

test("AC-201 authorization failure fails closed", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar", { open: false });
  const update = new UpdateRestaurantHandler({
    accessControl: {
      canRegisterRestaurant: async () => true,
      canManageRestaurant: async () => {
        throw new Error("authorization timeout");
      },
    },
    restaurants: world.restaurants,
    clock: world.clock,
  });

  await assert.rejects(
    update.execute({ subject: owner, restaurantId, name: "Ramen" }),
    /authorization timeout/,
  );
  assert.equal((await world.restaurants.get(restaurantId))?.name, "Pho Bar");
});

test("AC-207 open restaurant publishes RestaurantOpened in the same unit of work", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar", { open: false });
  const commitsBefore = world.unitOfWork.commits;

  const result = await world.open.execute({ subject: owner, restaurantId });

  assert.equal(result.isOpen, true);
  assert.equal(result.changed, true);
  assert.equal(world.unitOfWork.commits, commitsBefore + 1);
  assert.deepEqual(world.outbox.records, [
    {
      aggregate: { type: "Restaurant", id: restaurantId, version: 2 },
      event: {
        type: "RestaurantOpened",
        restaurantId,
        occurredAt: world.clock.now(),
      },
    },
  ]);
});

test("AC-209 opening an open restaurant is a no-op without a business fact", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");

  const result = await world.open.execute({ subject: owner, restaurantId });

  assert.equal(result.changed, false);
  assert.equal(result.version, 2);
  assert.deepEqual(world.outbox.eventTypes(), ["RestaurantOpened"]);
});

test("close restaurant publishes RestaurantClosed", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");

  const result = await world.close.execute({ subject: owner, restaurantId });

  assert.equal(result.isOpen, false);
  assert.deepEqual(world.outbox.eventTypes(), [
    "RestaurantOpened",
    "RestaurantClosed",
  ]);
});

test("AC-210 opening is not committed when the outbox write fails", async () => {
  const infrastructure = createInfrastructure();
  const failingOutbox: Outbox = {
    add: async () => {
      throw new Error("outbox unavailable");
    },
  };
  const world = createRestaurantWorld(infrastructure, {
    outbox: failingOutbox,
  });
  const restaurantId = await world.givenRestaurant("Pho Bar", { open: false });

  await assert.rejects(
    world.open.execute({ subject: owner, restaurantId }),
    /outbox unavailable/,
  );
  const restaurant = await world.restaurants.get(restaurantId);
  assert.equal(restaurant?.isOpen, false);
  assert.equal(restaurant?.version, 1);
  assert.equal(world.unitOfWork.rollbacks, 1);
});

test("AC-211 get restaurant returns the public representation", async () => {
  const world = createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");
  const get = new GetRestaurantHandler({ reader: world.catalog });

  assert.deepEqual(await get.execute({ restaurantId }), {
    restaurantId,
    name: "Pho Bar",
    isOpen: true,
    address: PICKUP_ADDRESS,
  });
});

test("get restaurant stored before addresses were required returns an empty address", async () => {
  const world = createRestaurantWorld();
  world.store.save("legacy", 0, {
    id: "legacy",
    name: "Old Pho",
    ownerId: owner.id,
    isOpen: true,
  });
  const get = new GetRestaurantHandler({ reader: world.catalog });

  assert.equal((await get.execute({ restaurantId: "legacy" })).address, "");
  assert.equal((await world.restaurants.get("legacy"))?.address, "");
});

test("AC-212 get unknown restaurant is not found", async () => {
  const world = createRestaurantWorld();
  const get = new GetRestaurantHandler({ reader: world.catalog });

  await assert.rejects(get.execute({ restaurantId: "missing" }), NotFoundError);
});

test("AC-213 list restaurants returns only open restaurants by default", async () => {
  const world = createRestaurantWorld();
  const open = await world.givenRestaurant("Open");
  const closed = await world.givenRestaurant("Closed", { open: false });
  const list = new ListRestaurantsHandler({ reader: world.catalog });

  const defaultPage = await list.execute({});
  const allPage = await list.execute({ includeClosed: true });

  assert.deepEqual(
    defaultPage.restaurants.map((restaurant) => restaurant.restaurantId),
    [open],
  );
  assert.deepEqual(
    allPage.restaurants.map((restaurant) => restaurant.restaurantId),
    [open, closed],
  );
});

test("AC-214 list restaurants pages with a cursor without repeats", async () => {
  const world = createRestaurantWorld();
  const ids = [
    await world.givenRestaurant("A"),
    await world.givenRestaurant("B"),
    await world.givenRestaurant("C"),
  ];
  const list = new ListRestaurantsHandler({ reader: world.catalog });

  const first = await list.execute({ limit: 2 });
  const second = await list.execute({ limit: 2, cursor: first.nextCursor });

  assert.deepEqual(
    first.restaurants.map((restaurant) => restaurant.restaurantId),
    ids.slice(0, 2),
  );
  assert.equal(first.nextCursor, ids[1]);
  assert.deepEqual(
    second.restaurants.map((restaurant) => restaurant.restaurantId),
    ids.slice(2),
  );
  assert.equal(second.nextCursor, undefined);
  await assert.rejects(list.execute({ limit: 0 }), InvalidPageSizeError);
  await assert.rejects(list.execute({ limit: 101 }), InvalidPageSizeError);
});
