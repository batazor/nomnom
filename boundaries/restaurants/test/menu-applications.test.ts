import assert from "node:assert/strict";
import test from "node:test";

import {
  AlreadyExistsError,
  NotFoundError,
  PermissionDeniedError,
} from "../pkg/application/errors.ts";
import type { Outbox } from "../pkg/application/outbox.ts";
import { InvalidMoneyError } from "../slices/menu/domains/menu/vo/money.ts";
import { createMenuWorld } from "./support/menus.ts";
import {
  createInfrastructure,
  owner,
  stranger,
} from "./support/restaurants.ts";

test("AC-221 create menu creates an empty menu of the restaurant", async () => {
  const world = createMenuWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");
  world.menuAccessControl.grantManager(owner, restaurantId);

  const result = await world.createMenu.execute({
    subject: owner,
    restaurantId,
  });

  assert.deepEqual(result, { menuId: "menu-1", restaurantId, version: 1 });
  assert.deepEqual(await world.getMenu.execute({ restaurantId }), {
    menuId: "menu-1",
    restaurantId,
    items: [],
  });
});

test("AC-222 a restaurant has at most one menu", async () => {
  const world = createMenuWorld();
  const restaurantId = await world.givenRestaurantWithMenu();

  await assert.rejects(
    world.createMenu.execute({ subject: owner, restaurantId }),
    AlreadyExistsError,
  );
  assert.equal(world.menuStore.all().length, 1);
});

test("AC-223 create menu for an unknown restaurant is not found", async () => {
  const world = createMenuWorld();
  world.menuAccessControl.grantManager(owner, "missing");

  await assert.rejects(
    world.createMenu.execute({ subject: owner, restaurantId: "missing" }),
    NotFoundError,
  );
});

test("AC-220 menu changes require the manage-menu permission", async () => {
  const world = createMenuWorld();
  const restaurantId = await world.givenRestaurantWithMenu();
  const menuItemId = await world.givenMenuItem(restaurantId, "Pho");

  await assert.rejects(
    world.addMenuItem.execute({
      subject: stranger,
      restaurantId,
      name: "Ramen",
      price: { amount: "8.00", currency: "EUR" },
    }),
    PermissionDeniedError,
  );
  await assert.rejects(
    world.setAvailability.execute({
      subject: stranger,
      restaurantId,
      menuItemId,
      isAvailable: false,
    }),
    PermissionDeniedError,
  );
  await assert.rejects(
    world.createMenu.execute({ subject: stranger, restaurantId }),
    PermissionDeniedError,
  );
  const menu = await world.getMenu.execute({ restaurantId });
  assert.deepEqual(
    menu.items.map((item) => [item.name, item.isAvailable]),
    [["Pho", true]],
  );
});

test("AC-226 add menu item creates an available dish by default", async () => {
  const world = createMenuWorld();
  const restaurantId = await world.givenRestaurantWithMenu();
  const outboxBefore = world.outbox.records.length;

  const result = await world.addMenuItem.execute({
    subject: owner,
    restaurantId,
    name: "Pho",
    description: "Beef noodle soup",
    price: { amount: "9.50", currency: "EUR" },
  });

  assert.deepEqual(result, {
    menuItemId: "menu-2",
    menuId: "menu-1",
    name: "Pho",
    description: "Beef noodle soup",
    price: { amount: "9.50", currency: "EUR" },
    isAvailable: true,
    version: 2,
    changed: true,
  });
  assert.equal(world.outbox.records.length, outboxBefore);
});

test("AC-224 negative price is rejected without changing the menu", async () => {
  const world = createMenuWorld();
  const restaurantId = await world.givenRestaurantWithMenu();

  await assert.rejects(
    world.addMenuItem.execute({
      subject: owner,
      restaurantId,
      name: "Pho",
      price: { amount: "-1.00", currency: "EUR" },
    }),
    InvalidMoneyError,
  );
  assert.deepEqual((await world.getMenu.execute({ restaurantId })).items, []);
});

test("AC-225 price keeps the exact decimal value", async () => {
  const world = createMenuWorld();
  const restaurantId = await world.givenRestaurantWithMenu();

  await world.addMenuItem.execute({
    subject: owner,
    restaurantId,
    name: "Tea",
    price: { amount: "0.10", currency: "EUR" },
  });

  const [item] = (await world.getMenu.execute({ restaurantId })).items;
  assert.deepEqual(item?.price, { amount: "0.10", currency: "EUR" });
});

test("AC-227 add menu item without a menu is not found", async () => {
  const world = createMenuWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");
  world.menuAccessControl.grantManager(owner, restaurantId);

  await assert.rejects(
    world.addMenuItem.execute({
      subject: owner,
      restaurantId,
      name: "Pho",
      price: { amount: "9.50", currency: "EUR" },
    }),
    NotFoundError,
  );
});

test("AC-228 update menu item changes the price without a business fact", async () => {
  const world = createMenuWorld();
  const restaurantId = await world.givenRestaurantWithMenu();
  const menuItemId = await world.givenMenuItem(restaurantId, "Pho");
  const outboxBefore = world.outbox.records.length;

  const result = await world.updateMenuItem.execute({
    subject: owner,
    restaurantId,
    menuItemId,
    price: { amount: "10.25", currency: "EUR" },
  });

  assert.equal(result.changed, true);
  assert.deepEqual(result.price, { amount: "10.25", currency: "EUR" });
  assert.equal(result.name, "Pho");
  assert.equal(result.isAvailable, true);
  assert.equal(world.outbox.records.length, outboxBefore);
});

test("update menu item availability publishes MenuItemAvailabilityChanged", async () => {
  const world = createMenuWorld();
  const restaurantId = await world.givenRestaurantWithMenu();
  const menuItemId = await world.givenMenuItem(restaurantId, "Pho");

  const result = await world.updateMenuItem.execute({
    subject: owner,
    restaurantId,
    menuItemId,
    name: "Pho Bo",
    isAvailable: false,
  });

  assert.equal(result.isAvailable, false);
  const last = world.outbox.records.at(-1);
  assert.equal(last?.event.type, "MenuItemAvailabilityChanged");
  assert.deepEqual(last?.aggregate, {
    type: "Menu",
    id: "menu-1",
    version: result.version,
  });
});

test("AC-229 update of an unknown menu item is not found", async () => {
  const world = createMenuWorld();
  const restaurantId = await world.givenRestaurantWithMenu();

  await assert.rejects(
    world.updateMenuItem.execute({
      subject: owner,
      restaurantId,
      menuItemId: "missing",
      name: "Pho",
    }),
    NotFoundError,
  );
});

test("AC-230 set availability changes only availability and publishes the fact", async () => {
  const world = createMenuWorld();
  const restaurantId = await world.givenRestaurantWithMenu();
  const menuItemId = await world.givenMenuItem(restaurantId, "Pho");
  const before = await world.getMenu.execute({ restaurantId });

  const result = await world.setAvailability.execute({
    subject: owner,
    restaurantId,
    menuItemId,
    isAvailable: false,
  });

  assert.equal(result.changed, true);
  const after = await world.getMenu.execute({ restaurantId });
  assert.deepEqual(after.items, [{ ...before.items[0], isAvailable: false }]);
  assert.deepEqual(world.outbox.records.at(-1)?.event, {
    type: "MenuItemAvailabilityChanged",
    menuId: "menu-1",
    restaurantId,
    menuItemId,
    isAvailable: false,
    occurredAt: world.clock.now(),
  });
});

test("AC-231 setting the current availability is a no-op", async () => {
  const world = createMenuWorld();
  const restaurantId = await world.givenRestaurantWithMenu();
  const menuItemId = await world.givenMenuItem(restaurantId, "Pho");
  const outboxBefore = world.outbox.records.length;
  const version = (await world.menus.findByRestaurantId(restaurantId))?.version;

  const result = await world.setAvailability.execute({
    subject: owner,
    restaurantId,
    menuItemId,
    isAvailable: true,
  });

  assert.equal(result.changed, false);
  assert.equal(result.version, version);
  assert.equal(world.outbox.records.length, outboxBefore);
});

test("availability change is rolled back when the outbox fails", async () => {
  const failingOutbox: Outbox = {
    add: async () => {
      throw new Error("outbox unavailable");
    },
  };
  const world = createMenuWorld(createInfrastructure(), {
    outbox: failingOutbox,
  });
  const restaurantId = await world.givenRestaurantWithMenu("Pho Bar", {
    open: false,
  });
  const menuItemId = await world.givenMenuItem(restaurantId, "Pho");

  await assert.rejects(
    world.setAvailability.execute({
      subject: owner,
      restaurantId,
      menuItemId,
      isAvailable: false,
    }),
    /outbox unavailable/,
  );
  const [item] = (await world.getMenu.execute({ restaurantId })).items;
  assert.equal(item?.isAvailable, true);
});

test("AC-232 get menu returns all items with current prices and availability", async () => {
  const world = createMenuWorld();
  const restaurantId = await world.givenRestaurantWithMenu();
  await world.givenMenuItem(restaurantId, "Pho");
  await world.givenMenuItem(restaurantId, "Ramen", false);

  const menu = await world.getMenu.execute({ restaurantId });

  assert.deepEqual(
    menu.items.map((item) => [item.name, item.price.amount, item.isAvailable]),
    [
      ["Pho", "9.50", true],
      ["Ramen", "9.50", false],
    ],
  );
});

test("AC-233 get menu of a restaurant without a menu is not found", async () => {
  const world = createMenuWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");

  await assert.rejects(world.getMenu.execute({ restaurantId }), NotFoundError);
});
