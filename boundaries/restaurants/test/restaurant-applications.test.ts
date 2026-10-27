import assert from "node:assert/strict";
import test from "node:test";

import {
  NotFoundError,
  PermissionDeniedError,
} from "../pkg/application/errors.ts";
import type { Outbox } from "../pkg/application/outbox.ts";
import { InvalidMoneyError } from "../slices/menu/domains/menu/vo/money.ts";
import { GetRestaurantHandler } from "../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import { ListOwnedRestaurantsHandler } from "../slices/restaurant/applications/list-owned-restaurants/list-owned-restaurants.ts";
import {
  InvalidPageSizeError,
  ListRestaurantsHandler,
} from "../slices/restaurant/applications/list-restaurants/list-restaurants.ts";
import { RegisterRestaurantHandler } from "../slices/restaurant/applications/register-restaurant/register-restaurant.ts";
import {
  type PremiumFeeChange,
  UpdateRestaurantHandler,
} from "../slices/restaurant/applications/update-restaurant/update-restaurant.ts";
import {
  InvalidMarketIdError,
  InvalidRestaurantError,
} from "../slices/restaurant/domains/restaurant/restaurant.ts";
import {
  createInfrastructure,
  createRestaurantWorld,
  market,
  operator,
  otherMarket,
  owner,
  stranger,
} from "./support/restaurants.ts";

test("AC-202 register restaurant creates a closed restaurant after the owner relationship", async () => {
  const world = await createRestaurantWorld();
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
        return "zed-owner";
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
    marketId: market,
  });

  assert.deepEqual(result, {
    restaurantId: "restaurant-1",
    name: "Pho Bar",
    marketId: market,
    isOpen: false,
    version: 1,
  });
  assert.deepEqual(calls, ["touch:restaurant-1"]);
  assert.deepEqual(world.authorization.owners, [
    { restaurantId: "restaurant-1", ownerId: owner.id },
  ]);
  const stored = await world.restaurants.get("restaurant-1");
  assert.equal(stored?.isOpen, false);
  assert.equal(stored?.marketId.value, market);
  // FR-447: the owner TOUCH revision is saved with the new restaurant.
  assert.equal(stored?.authorizationRevision, "zed-owner");
  assert.deepEqual(await world.outbox.records(), []);
});

test("register restaurant requires the registration permission", async () => {
  const world = await createRestaurantWorld();

  await assert.rejects(
    world.register.execute({
      subject: stranger,
      name: "Pho",
      ownerId: owner.id,
      marketId: market,
    }),
    PermissionDeniedError,
  );
  assert.deepEqual(await world.store.find({}), []);
  assert.deepEqual(world.authorization.owners, []);
});

test("AC-203 register restaurant rejects a blank name", async () => {
  const world = await createRestaurantWorld();

  await assert.rejects(
    world.register.execute({
      subject: operator,
      name: "  ",
      ownerId: owner.id,
      marketId: market,
    }),
    InvalidRestaurantError,
  );
  assert.deepEqual(await world.store.find({}), []);
});

test("register restaurant requires a valid Market (ADR 41)", async () => {
  const world = await createRestaurantWorld();

  for (const marketId of ["", "Berlin", "b", "berlin_1", "9berlin"]) {
    await assert.rejects(
      world.register.execute({
        subject: operator,
        name: "Pho",
        ownerId: owner.id,
        marketId,
      }),
      InvalidMarketIdError,
    );
  }
  assert.deepEqual(await world.store.find({}), []);
  assert.deepEqual(world.authorization.owners, []);
});

test("AC-204 restaurant is not stored when the owner relationship write fails", async () => {
  const world = await createRestaurantWorld();
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
      marketId: market,
    }),
    /spicedb unavailable/,
  );
  assert.deepEqual(await world.store.find({}), []);
});

test("AC-205 update restaurant renames without touching the opening", async () => {
  const world = await createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");
  const update = new UpdateRestaurantHandler({
    accessControl: world.authorization,
    restaurants: world.restaurants,
    clock: world.clock,
  });
  const outboxBefore = (await world.outbox.records()).length;

  const result = await update.execute({
    subject: owner,
    restaurantId,
    name: "Ramen Bar",
    premiumFee: { kind: "keep" },
  });
  const unchanged = await update.execute({
    subject: owner,
    restaurantId,
    name: "Ramen Bar",
    premiumFee: { kind: "keep" },
  });

  assert.deepEqual(result, {
    restaurantId,
    name: "Ramen Bar",
    marketId: market,
    isOpen: true,
    version: 3,
    changed: true,
  });
  assert.equal(unchanged.changed, false);
  assert.equal(unchanged.version, 3);
  assert.equal((await world.outbox.records()).length, outboxBefore);
});

test("AC-849 AC-850 update restaurant sets and clears the premium fee", async () => {
  const world = await createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");
  const get = new GetRestaurantHandler({ reader: world.catalog });
  const updateFee = (premiumFee: PremiumFeeChange) =>
    world.update.execute({
      subject: owner,
      restaurantId,
      name: "Pho Bar",
      premiumFee,
    });

  const set = await updateFee({
    kind: "set",
    fee: { amount: "99.00", currency: "RUB" },
  });
  const kept = await updateFee({ kind: "keep" });
  const repeated = await updateFee({
    kind: "set",
    fee: { amount: "99.00", currency: "RUB" },
  });

  assert.equal(set.changed, true);
  assert.deepEqual(set.premiumFee, { amount: "99.00", currency: "RUB" });
  assert.deepEqual([kept.changed, repeated.changed], [false, false]);
  assert.deepEqual((await get.execute({ restaurantId })).premiumFee, {
    amount: "99.00",
    currency: "RUB",
  });

  const cleared = await updateFee({ kind: "clear" });

  assert.equal(cleared.changed, true);
  assert.equal(cleared.premiumFee, undefined);
  assert.equal(
    (await get.execute({ restaurantId })).premiumFee,
    undefined,
    "no premium is offered",
  );
});

test("AC-851 update restaurant rejects a zero or malformed premium fee", async () => {
  const world = await createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");
  const before = await world.restaurants.get(restaurantId);

  for (const amount of ["0", "0.00", "-1.00"]) {
    await assert.rejects(
      world.update.execute({
        subject: owner,
        restaurantId,
        name: "Pho Bar",
        premiumFee: { kind: "set", fee: { amount, currency: "RUB" } },
      }),
      (error: unknown) =>
        error instanceof InvalidRestaurantError ||
        error instanceof InvalidMoneyError,
    );
  }
  const after = await world.restaurants.get(restaurantId);
  assert.equal(after?.version, before?.version);
  assert.equal(after?.premiumFee, undefined);
});

test("AC-206 update of an unknown restaurant is not found", async () => {
  const world = await createRestaurantWorld();
  world.authorization.grantManager(owner, "missing");
  const update = new UpdateRestaurantHandler({
    accessControl: world.authorization,
    restaurants: world.restaurants,
    clock: world.clock,
  });

  await assert.rejects(
    update.execute({
      subject: owner,
      restaurantId: "missing",
      name: "Pho",
      premiumFee: { kind: "keep" },
    }),
    NotFoundError,
  );
});

test("AC-200 manager commands are denied without the manage permission", async () => {
  const world = await createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar", { open: false });
  const update = new UpdateRestaurantHandler({
    accessControl: world.authorization,
    restaurants: world.restaurants,
    clock: world.clock,
  });

  await assert.rejects(
    update.execute({
      subject: stranger,
      restaurantId,
      name: "Ramen",
      premiumFee: { kind: "keep" },
    }),
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
  assert.deepEqual(await world.outbox.records(), []);
});

test("AC-201 authorization failure fails closed", async () => {
  const world = await createRestaurantWorld();
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
    update.execute({
      subject: owner,
      restaurantId,
      name: "Ramen",
      premiumFee: { kind: "keep" },
    }),
    /authorization timeout/,
  );
  assert.equal((await world.restaurants.get(restaurantId))?.name, "Pho Bar");
});

test("AC-207 open restaurant publishes RestaurantOpened in the same unit of work", async () => {
  const world = await createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar", { open: false });

  const result = await world.open.execute({ subject: owner, restaurantId });

  assert.equal(result.isOpen, true);
  assert.equal(result.changed, true);
  assert.deepEqual(await world.outbox.records(), [
    {
      aggregate: { type: "Restaurant", id: restaurantId, version: 2 },
      event: {
        type: "RestaurantOpened",
        restaurantId,
        marketId: market,
        occurredAt: world.clock.now(),
      },
    },
  ]);
});

test("AC-209 opening an open restaurant is a no-op without a business fact", async () => {
  const world = await createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");

  const result = await world.open.execute({ subject: owner, restaurantId });

  assert.equal(result.changed, false);
  assert.equal(result.version, 2);
  assert.deepEqual(await world.outbox.eventTypes(), ["RestaurantOpened"]);
});

test("close restaurant publishes RestaurantClosed", async () => {
  const world = await createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");

  const result = await world.close.execute({ subject: owner, restaurantId });

  assert.equal(result.isOpen, false);
  assert.deepEqual(await world.outbox.eventTypes(), [
    "RestaurantOpened",
    "RestaurantClosed",
  ]);
});

test("AC-210 opening is not committed when the outbox write fails", async () => {
  const infrastructure = await createInfrastructure();
  const failingOutbox: Outbox = {
    add: async () => {
      throw new Error("outbox unavailable");
    },
  };
  const world = await createRestaurantWorld(infrastructure, {
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
});

test("AC-211 get restaurant returns the public representation", async () => {
  const world = await createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");
  const get = new GetRestaurantHandler({ reader: world.catalog });

  assert.deepEqual(await get.execute({ restaurantId }), {
    restaurantId,
    name: "Pho Bar",
    marketId: market,
    isOpen: true,
    // FR-204: no history yet, the wait is the preparation time.
    waitTime: { elevated: false, expectedWaitMinutes: 20 },
  });
});

test("AC-212 get unknown restaurant is not found", async () => {
  const world = await createRestaurantWorld();
  const get = new GetRestaurantHandler({ reader: world.catalog });

  await assert.rejects(get.execute({ restaurantId: "missing" }), NotFoundError);
});

test("AC-213 list restaurants returns only open restaurants by default", async () => {
  const world = await createRestaurantWorld();
  const open = await world.givenRestaurant("Open");
  const closed = await world.givenRestaurant("Closed", { open: false });
  const list = new ListRestaurantsHandler({ reader: world.catalog });

  const defaultPage = await list.execute({ marketId: market });
  const allPage = await list.execute({ marketId: market, includeClosed: true });

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
  const world = await createRestaurantWorld();
  const ids = [
    await world.givenRestaurant("A"),
    await world.givenRestaurant("B"),
    await world.givenRestaurant("C"),
  ];
  const list = new ListRestaurantsHandler({ reader: world.catalog });

  const first = await list.execute({ marketId: market, limit: 2 });
  const second = await list.execute({
    marketId: market,
    limit: 2,
    cursor: first.nextCursor,
  });

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
  await assert.rejects(
    list.execute({ marketId: market, limit: 0 }),
    InvalidPageSizeError,
  );
  await assert.rejects(
    list.execute({ marketId: market, limit: 101 }),
    InvalidPageSizeError,
  );
});

test("list restaurants returns only restaurants of the requested Market (ADR 41)", async () => {
  const world = await createRestaurantWorld();
  const berlin = await world.givenRestaurant("Berlin Pho");
  const hamburg = await world.givenRestaurant("Hamburg Pho", {
    open: true,
    marketId: otherMarket,
  });
  const list = new ListRestaurantsHandler({ reader: world.catalog });

  const berlinPage = await list.execute({ marketId: market });
  const hamburgPage = await list.execute({
    marketId: otherMarket,
    includeClosed: true,
  });
  const emptyPage = await list.execute({ marketId: "munich" });

  assert.deepEqual(
    berlinPage.restaurants.map((restaurant) => [
      restaurant.restaurantId,
      restaurant.marketId,
    ]),
    [[berlin, market]],
  );
  assert.deepEqual(
    hamburgPage.restaurants.map((restaurant) => [
      restaurant.restaurantId,
      restaurant.marketId,
    ]),
    [[hamburg, otherMarket]],
  );
  assert.deepEqual(emptyPage.restaurants, []);
  for (const marketId of ["", "BERLIN", "berlin/1"]) {
    await assert.rejects(list.execute({ marketId }), InvalidMarketIdError);
  }
});

test("list owned restaurants returns open and closed restaurants of the owner only", async () => {
  const world = await createRestaurantWorld();
  const open = await world.givenRestaurant("Open");
  const closed = await world.givenRestaurant("Closed", { open: false });
  await world.givenRestaurant("Other Market", {
    open: true,
    marketId: otherMarket,
  });
  await world.register.execute({
    subject: operator,
    name: "Foreign",
    ownerId: stranger.id,
    marketId: market,
  });
  const list = new ListOwnedRestaurantsHandler({ reader: world.catalog });

  const first = await list.execute({
    subject: owner,
    marketId: market,
    limit: 1,
  });
  const second = await list.execute({
    subject: owner,
    marketId: market,
    limit: 1,
    cursor: first.nextCursor,
  });
  const strangers = await list.execute({
    subject: { type: "user", id: "nobody" },
    marketId: market,
  });

  assert.deepEqual(
    [...first.restaurants, ...second.restaurants].map(
      (restaurant) => restaurant.restaurantId,
    ),
    [open, closed].sort(),
  );
  assert.equal(second.nextCursor, undefined);
  assert.deepEqual(strangers.restaurants, []);
  await assert.rejects(
    list.execute({
      subject: { type: "service", id: "orders" },
      marketId: market,
    }),
    PermissionDeniedError,
  );
  await assert.rejects(
    list.execute({ subject: owner, marketId: market, limit: 101 }),
    InvalidPageSizeError,
  );
  await assert.rejects(
    list.execute({ subject: owner, marketId: "" }),
    InvalidMarketIdError,
  );
});
