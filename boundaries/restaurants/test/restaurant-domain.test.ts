import assert from "node:assert/strict";
import test from "node:test";

import {
  InvalidRestaurantError,
  InvalidRestaurantReason,
  Restaurant,
} from "../slices/restaurant/domains/restaurant/restaurant.ts";

const at = new Date(Date.UTC(2026, 9, 25, 9));

const register = () =>
  Restaurant.register({
    id: "r-1",
    name: "  Pho Bar ",
    ownerId: "u-owner",
    address: " 1 Pho Street ",
    registeredAt: at,
  });

test("registered restaurant is closed, named and records its registration", () => {
  const restaurant = register();

  assert.equal(restaurant.name, "Pho Bar");
  assert.equal(restaurant.address, "1 Pho Street");
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
        address: "1 Pho Street",
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
        address: "1 Pho Street",
        registeredAt: at,
      }),
    (error: unknown) =>
      error instanceof InvalidRestaurantError &&
      error.reason === InvalidRestaurantReason.EmptyOwner,
  );
  assert.throws(() => register().rename("", at), InvalidRestaurantError);
});

test("restaurant address must not be blank", () => {
  assert.throws(
    () =>
      Restaurant.register({
        id: "r-1",
        name: "Pho",
        ownerId: "u-owner",
        address: "  ",
        registeredAt: at,
      }),
    (error: unknown) =>
      error instanceof InvalidRestaurantError &&
      error.reason === InvalidRestaurantReason.EmptyAddress,
  );
  assert.throws(
    () => register().changeAddress(" ", at),
    (error: unknown) =>
      error instanceof InvalidRestaurantError &&
      error.reason === InvalidRestaurantReason.EmptyAddress,
  );
});

test("change address to the current address is a no-op", () => {
  const restaurant = register();
  restaurant.pullEvents();

  assert.equal(restaurant.changeAddress("1 Pho Street", at), false);
  assert.equal(restaurant.changeAddress(" 2 Ramen Lane ", at), true);
  assert.equal(restaurant.address, "2 Ramen Lane");
  assert.deepEqual(
    restaurant.pullEvents().map((event) => event.type),
    ["RestaurantAddressChanged"],
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

test("restore keeps state and version without events", () => {
  const restaurant = Restaurant.restore(
    {
      id: "r-1",
      name: "Pho",
      ownerId: "u-1",
      isOpen: true,
      address: "1 Pho Street",
    },
    3,
  );

  assert.equal(restaurant.version, 3);
  assert.equal(restaurant.isOpen, true);
  assert.equal(restaurant.address, "1 Pho Street");
  assert.deepEqual(restaurant.pullEvents(), []);
});

test("restore of a restaurant stored without an address yields an empty address", () => {
  const restaurant = Restaurant.restore(
    { id: "r-1", name: "Pho", ownerId: "u-1", isOpen: true },
    3,
  );

  assert.equal(restaurant.address, "");
  assert.equal(restaurant.toSnapshot().address, "");
});
