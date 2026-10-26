import assert from "node:assert/strict";
import test from "node:test";

import {
  InvalidMarketIdError,
  InvalidRestaurantError,
  InvalidRestaurantReason,
  MarketId,
  Restaurant,
} from "../slices/restaurant/domains/restaurant/restaurant.ts";

const at = new Date(Date.UTC(2026, 9, 25, 9));

const register = () =>
  Restaurant.register({
    id: "r-1",
    name: "  Pho Bar ",
    ownerId: "u-owner",
    marketId: "berlin",
    registeredAt: at,
  });

test("registered restaurant is closed, named and records its registration", () => {
  const restaurant = register();

  assert.equal(restaurant.name, "Pho Bar");
  assert.equal(restaurant.isOpen, false);
  assert.equal(restaurant.version, 0);
  assert.deepEqual(
    restaurant.pullEvents().map((event) => event.type),
    ["RestaurantRegistered"],
  );
  assert.deepEqual(restaurant.pullEvents(), []);
});

test("restaurant name and owner must not be blank", () => {
  assert.throws(
    () =>
      Restaurant.register({
        id: "r-1",
        name: "   ",
        ownerId: "u-owner",
        marketId: "berlin",
        registeredAt: at,
      }),
    (error: unknown) =>
      error instanceof InvalidRestaurantError &&
      error.reason === InvalidRestaurantReason.EmptyName,
  );
  assert.throws(
    () =>
      Restaurant.register({
        id: "r-1",
        name: "Pho",
        ownerId: " ",
        marketId: "berlin",
        registeredAt: at,
      }),
    (error: unknown) =>
      error instanceof InvalidRestaurantError &&
      error.reason === InvalidRestaurantReason.EmptyOwner,
  );
  assert.throws(() => register().rename("", at), InvalidRestaurantError);
});

test("ADR 41 restaurant belongs to one immutable Market carried by its events", () => {
  const restaurant = register();
  restaurant.open(at);
  restaurant.close(at);

  assert.equal(restaurant.marketId.value, "berlin");
  assert.equal(restaurant.toSnapshot().marketId, "berlin");
  assert.deepEqual(
    restaurant.pullEvents().map((event) => [event.type, event.marketId]),
    [
      ["RestaurantRegistered", "berlin"],
      ["RestaurantOpened", "berlin"],
      ["RestaurantClosed", "berlin"],
    ],
  );
  assert.equal(
    Restaurant.restore(restaurant.toSnapshot(), 1).marketId.value,
    "berlin",
  );
});

test("ADR 41 Market id is a slug", () => {
  for (const valid of ["berlin", "new-york", "b2", `a${"b".repeat(31)}`]) {
    assert.equal(MarketId.of(valid).value, valid);
  }
  for (const invalid of [
    "",
    "b",
    "Berlin",
    "1berlin",
    "-berlin",
    "berlin_east",
    " berlin",
    `a${"b".repeat(32)}`,
  ]) {
    assert.throws(() => MarketId.of(invalid), InvalidMarketIdError);
  }
  assert.ok(MarketId.of("berlin").equals(MarketId.of("berlin")));
  assert.ok(!MarketId.of("berlin").equals(MarketId.of("hamburg")));
  assert.throws(
    () =>
      Restaurant.register({
        id: "r-1",
        name: "Pho",
        ownerId: "u-owner",
        marketId: "",
        registeredAt: at,
      }),
    InvalidMarketIdError,
  );
});

test("open and close change the opening once and repeat as no-op", () => {
  const restaurant = register();
  restaurant.pullEvents();

  assert.equal(restaurant.open(at), true);
  assert.equal(restaurant.open(at), false);
  assert.equal(restaurant.isOpen, true);
  assert.equal(restaurant.close(at), true);
  assert.equal(restaurant.close(at), false);
  assert.equal(restaurant.isOpen, false);
  assert.deepEqual(
    restaurant.pullEvents().map((event) => event.type),
    ["RestaurantOpened", "RestaurantClosed"],
  );
});

test("rename to the current name is a no-op", () => {
  const restaurant = register();
  restaurant.pullEvents();

  assert.equal(restaurant.rename("Pho Bar", at), false);
  assert.equal(restaurant.rename("Ramen Bar", at), true);
  assert.equal(restaurant.name, "Ramen Bar");
  assert.deepEqual(
    restaurant.pullEvents().map((event) => event.type),
    ["RestaurantRenamed"],
  );
});

test("FR-447 authorization revision is technical snapshot state; old snapshots load without it", () => {
  const legacy = Restaurant.restore(
    {
      id: "r-1",
      name: "Pho",
      ownerId: "u-owner",
      marketId: "berlin",
      isOpen: false,
    },
    3,
  );
  assert.equal(legacy.authorizationRevision, undefined);
  assert.equal("authorizationRevision" in legacy.toSnapshot(), false);

  legacy.recordAuthorizationRevision("zed-1");

  assert.equal(legacy.toSnapshot().authorizationRevision, "zed-1");
  assert.deepEqual(legacy.pullEvents(), []);
  assert.equal(
    Restaurant.restore(legacy.toSnapshot(), 4).authorizationRevision,
    "zed-1",
  );
  assert.throws(() => legacy.recordAuthorizationRevision(""));
});
