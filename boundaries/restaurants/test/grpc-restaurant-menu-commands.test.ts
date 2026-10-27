import assert from "node:assert/strict";
import test from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code } from "@connectrpc/connect";

import {
  AddMenuItemRequestSchema,
  CloseRestaurantRequestSchema,
  CreateMenuRequestSchema,
  OpenRestaurantRequestSchema,
  SetMenuItemAvailabilityRequestSchema,
  UpdateMenuItemRequestSchema,
} from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { AuthorizationUnavailableError } from "../pkg/application/errors.ts";
import type { Subject } from "../pkg/application/subject.ts";
import { context, createService, rejectsWith } from "./support/grpc.ts";
import { createMenuWorld } from "./support/menus.ts";
import { owner, stranger } from "./support/restaurants.ts";

const unused = {
  execute: async (): Promise<never> => assert.fail("unexpected call"),
};

/** Restaurant and menu command RPCs over the MongoDB world; `as` switches the caller. */
const createWorld = async () => {
  const world = await createMenuWorld();
  let caller: Subject = owner;
  const service = createService({
    getRestaurant: unused,
    getMenu: world.getMenu,
    batchGetRestaurants: unused,
    batchGetMenus: unused,
    listRestaurants: unused,
    listOwnedRestaurants: unused,
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
    listActiveRestaurantOrders: unused,
    registerRestaurant: unused,
    updateRestaurant: unused,
    assignStaff: unused,
    revokeStaff: unused,
    openRestaurant: world.open,
    closeRestaurant: world.close,
    createMenu: world.createMenu,
    addMenuItem: world.addMenuItem,
    updateMenuItem: world.updateMenuItem,
    setMenuItemAvailability: world.setAvailability,
    getSimulator: { execute: () => Promise.reject(new Error("unused")) },
    setSimulatorEnabled: { execute: () => Promise.reject(new Error("unused")) },
    hireCook: { execute: () => Promise.reject(new Error("unused")) },
    putCookOnLine: { execute: () => Promise.reject(new Error("unused")) },
    takeCookOffLine: { execute: () => Promise.reject(new Error("unused")) },
    retireCook: { execute: () => Promise.reject(new Error("unused")) },
    getCookPool: { execute: () => Promise.reject(new Error("unused")) },
    subjects: { resolve: async () => caller },
  });
  const as = (subject: Subject) => {
    caller = subject;
    return service;
  };
  /** Facts of the Menu aggregate written to the Outbox. */
  const menuFacts = async () =>
    (await world.outbox.records())
      .filter((record) => record.aggregate.type === "Menu")
      .map((record) => record.event.type);
  return { world, as, menuFacts };
};

const price = { amount: "9.50", currency: "EUR" };

test("FR-203 OpenRestaurant opens a closed restaurant and writes RestaurantOpened", async () => {
  const { world, as } = await createWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar", { open: false });

  const response = await as(owner).openRestaurant(
    create(OpenRestaurantRequestSchema, { restaurantId }),
    context,
  );

  assert.equal(response.changed, true);
  assert.equal(response.restaurant?.restaurantId, restaurantId);
  assert.equal(response.restaurant?.isOpen, true);
  assert.equal(response.version, 2n);
  assert.deepEqual(await world.outbox.eventTypes(), ["RestaurantOpened"]);
});

test("AC-209 AC-712 a repeated OpenRestaurant succeeds without a new fact", async () => {
  const { world, as } = await createWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar", { open: true });
  const before = (await world.outbox.eventTypes()).length;

  const response = await as(owner).openRestaurant(
    create(OpenRestaurantRequestSchema, { restaurantId }),
    context,
  );

  assert.equal(response.changed, false);
  assert.equal(response.restaurant?.isOpen, true);
  assert.equal((await world.outbox.eventTypes()).length, before);
});

test("FR-203 CloseRestaurant closes an open restaurant and a repeat is a no-op", async () => {
  const { world, as } = await createWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar", { open: true });

  const closed = await as(owner).closeRestaurant(
    create(CloseRestaurantRequestSchema, { restaurantId }),
    context,
  );
  assert.equal(closed.changed, true);
  assert.equal(closed.restaurant?.isOpen, false);
  assert.equal((await world.outbox.eventTypes()).at(-1), "RestaurantClosed");

  const repeated = await as(owner).closeRestaurant(
    create(CloseRestaurantRequestSchema, { restaurantId }),
    context,
  );
  assert.equal(repeated.changed, false);
  assert.equal(repeated.version, closed.version);
  assert.equal(
    (await world.outbox.eventTypes()).filter(
      (type) => type === "RestaurantClosed",
    ).length,
    1,
  );
});

test("AC-200 AC-713 opening commands without restaurant#manage are PERMISSION_DENIED", async () => {
  const { world, as } = await createWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar", { open: false });

  await assert.rejects(
    async () =>
      as(stranger).openRestaurant(
        create(OpenRestaurantRequestSchema, { restaurantId }),
        context,
      ),
    rejectsWith(Code.PermissionDenied),
  );
  await assert.rejects(
    async () =>
      as(stranger).closeRestaurant(
        create(CloseRestaurantRequestSchema, { restaurantId }),
        context,
      ),
    rejectsWith(Code.PermissionDenied),
  );
  assert.deepEqual(await world.outbox.eventTypes(), []);
});

test("opening commands map a missing restaurant to NOT_FOUND and a blank id to INVALID_ARGUMENT", async () => {
  const { world, as } = await createWorld();
  world.authorization.grantManager(owner, "missing");

  await assert.rejects(
    async () =>
      as(owner).openRestaurant(
        create(OpenRestaurantRequestSchema, { restaurantId: "missing" }),
        context,
      ),
    rejectsWith(Code.NotFound),
  );
  await assert.rejects(
    async () =>
      as(owner).closeRestaurant(
        create(CloseRestaurantRequestSchema, { restaurantId: " " }),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
  );
});

test("opening commands map unavailable authorization to UNAVAILABLE", async () => {
  const { world, as } = await createWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar", { open: false });
  world.authorization.canManageRestaurant = async () => {
    throw new AuthorizationUnavailableError();
  };

  await assert.rejects(
    async () =>
      as(owner).openRestaurant(
        create(OpenRestaurantRequestSchema, { restaurantId }),
        context,
      ),
    rejectsWith(Code.Unavailable),
  );
});

test("FR-211 AC-716 CreateMenu creates the single menu and a repeat is ALREADY_EXISTS", async () => {
  const { world, as } = await createWorld();
  const restaurantId = await world.givenRestaurant("Pho Bar");
  world.menuAccessControl.grantManager(owner, restaurantId);

  const created = await as(owner).createMenu(
    create(CreateMenuRequestSchema, { restaurantId }),
    context,
  );
  assert.equal(created.restaurantId, restaurantId);
  assert.notEqual(created.menuId, "");
  assert.equal(created.version, 1n);

  await assert.rejects(
    async () =>
      as(owner).createMenu(
        create(CreateMenuRequestSchema, { restaurantId }),
        context,
      ),
    rejectsWith(Code.AlreadyExists),
  );
});

test("AC-223 CreateMenu of a missing restaurant is NOT_FOUND", async () => {
  const { world, as } = await createWorld();
  world.menuAccessControl.grantManager(owner, "missing");

  await assert.rejects(
    async () =>
      as(owner).createMenu(
        create(CreateMenuRequestSchema, { restaurantId: "missing" }),
        context,
      ),
    rejectsWith(Code.NotFound),
  );
});

test("FR-213 AddMenuItem adds an available item by default", async () => {
  const { world, as } = await createWorld();
  const restaurantId = await world.givenRestaurantWithMenu();

  const response = await as(owner).addMenuItem(
    create(AddMenuItemRequestSchema, {
      restaurantId,
      name: "Pho",
      description: "Beef noodle soup",
      price,
    }),
    context,
  );

  assert.equal(response.menuItem?.name, "Pho");
  assert.equal(response.menuItem?.description, "Beef noodle soup");
  assert.equal(response.menuItem?.available, true);
  assert.deepEqual(
    {
      amount: response.menuItem?.price?.amount,
      currency: response.menuItem?.price?.currency,
    },
    price,
  );
  const menu = await world.getMenu.execute({ restaurantId });
  assert.deepEqual(
    menu.items.map((item) => item.menuItemId),
    [response.menuItem?.menuItemId],
  );
});

test("AddMenuItem maps invalid input and a missing menu to gRPC errors", async () => {
  const { world, as } = await createWorld();
  const restaurantId = await world.givenRestaurantWithMenu();

  await assert.rejects(
    async () =>
      as(owner).addMenuItem(
        create(AddMenuItemRequestSchema, { restaurantId, name: "Pho" }),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
    "price is required",
  );
  await assert.rejects(
    async () =>
      as(owner).addMenuItem(
        create(AddMenuItemRequestSchema, {
          restaurantId,
          name: "Pho",
          price: { amount: "-1.00", currency: "EUR" },
        }),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
    "AC-224 negative price",
  );
  await assert.rejects(
    async () =>
      as(owner).addMenuItem(
        create(AddMenuItemRequestSchema, { restaurantId, name: " ", price }),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
    "blank name",
  );

  const withoutMenu = await world.givenRestaurant("No Menu");
  world.menuAccessControl.grantManager(owner, withoutMenu);
  await assert.rejects(
    async () =>
      as(owner).addMenuItem(
        create(AddMenuItemRequestSchema, {
          restaurantId: withoutMenu,
          name: "Pho",
          price,
        }),
        context,
      ),
    rejectsWith(Code.NotFound),
    "AC-227 menu is missing",
  );
});

test("FR-214 FR-613 UpdateMenuItem changes only the set fields and publishes its facts", async () => {
  const { world, as, menuFacts } = await createWorld();
  const restaurantId = await world.givenRestaurantWithMenu();
  const menuItemId = await world.givenMenuItem(restaurantId, "Pho");
  assert.deepEqual(await menuFacts(), ["MenuItemAdded"], "AC-773");

  const renamed = await as(owner).updateMenuItem(
    create(UpdateMenuItemRequestSchema, {
      restaurantId,
      menuItemId,
      price: { amount: "11.00", currency: "EUR" },
    }),
    context,
  );
  assert.equal(renamed.changed, true);
  assert.equal(renamed.menuItem?.name, "Pho");
  assert.equal(renamed.menuItem?.price?.amount, "11.00");
  assert.deepEqual(
    await menuFacts(),
    ["MenuItemAdded", "MenuItemUpdated"],
    "AC-774 price change",
  );

  const unavailable = await as(owner).updateMenuItem(
    create(UpdateMenuItemRequestSchema, {
      restaurantId,
      menuItemId,
      name: "Pho Bo",
      available: false,
    }),
    context,
  );
  assert.equal(unavailable.menuItem?.name, "Pho Bo");
  assert.equal(unavailable.menuItem?.available, false);
  assert.deepEqual(await menuFacts(), [
    "MenuItemAdded",
    "MenuItemUpdated",
    "MenuItemUpdated",
    "MenuItemAvailabilityChanged",
  ]);

  const repeated = await as(owner).updateMenuItem(
    create(UpdateMenuItemRequestSchema, {
      restaurantId,
      menuItemId,
      name: "Pho Bo",
    }),
    context,
  );
  assert.equal(repeated.changed, false);
  assert.equal(repeated.version, unavailable.version);
});

test("AC-229 UpdateMenuItem of an unknown item is NOT_FOUND", async () => {
  const { world, as } = await createWorld();
  const restaurantId = await world.givenRestaurantWithMenu();

  await assert.rejects(
    async () =>
      as(owner).updateMenuItem(
        create(UpdateMenuItemRequestSchema, {
          restaurantId,
          menuItemId: "missing",
          name: "Pho",
        }),
        context,
      ),
    rejectsWith(Code.NotFound),
  );
});

test("FR-215 AC-714 SetMenuItemAvailability writes the fact once and a repeat is a no-op", async () => {
  const { world, as, menuFacts } = await createWorld();
  const restaurantId = await world.givenRestaurantWithMenu();
  const menuItemId = await world.givenMenuItem(restaurantId, "Pho");

  const changed = await as(owner).setMenuItemAvailability(
    create(SetMenuItemAvailabilityRequestSchema, {
      restaurantId,
      menuItemId,
      available: false,
    }),
    context,
  );
  assert.equal(changed.changed, true);
  assert.equal(changed.menuItem?.available, false);
  assert.deepEqual(await menuFacts(), [
    "MenuItemAdded",
    "MenuItemAvailabilityChanged",
  ]);

  const repeated = await as(owner).setMenuItemAvailability(
    create(SetMenuItemAvailabilityRequestSchema, {
      restaurantId,
      menuItemId,
      available: false,
    }),
    context,
  );
  assert.equal(repeated.changed, false, "AC-231");
  assert.equal(repeated.version, changed.version);
  assert.deepEqual(await menuFacts(), [
    "MenuItemAdded",
    "MenuItemAvailabilityChanged",
  ]);
});

test("AC-220 AC-715 menu commands without restaurant#manage_menu are PERMISSION_DENIED", async () => {
  const { world, as, menuFacts } = await createWorld();
  const restaurantId = await world.givenRestaurantWithMenu();
  const menuItemId = await world.givenMenuItem(restaurantId, "Pho");
  const menuBefore = await world.getMenu.execute({ restaurantId });

  const calls = [
    () =>
      as(stranger).createMenu(
        create(CreateMenuRequestSchema, { restaurantId }),
        context,
      ),
    () =>
      as(stranger).addMenuItem(
        create(AddMenuItemRequestSchema, { restaurantId, name: "Bun", price }),
        context,
      ),
    () =>
      as(stranger).updateMenuItem(
        create(UpdateMenuItemRequestSchema, {
          restaurantId,
          menuItemId,
          name: "Bun",
        }),
        context,
      ),
    () =>
      as(stranger).setMenuItemAvailability(
        create(SetMenuItemAvailabilityRequestSchema, {
          restaurantId,
          menuItemId,
          available: false,
        }),
        context,
      ),
  ];
  for (const call of calls) {
    await assert.rejects(call, rejectsWith(Code.PermissionDenied));
  }
  assert.deepEqual(await world.getMenu.execute({ restaurantId }), menuBefore);
  // Only the setup's MenuItemAdded: denied commands write nothing.
  assert.deepEqual(await menuFacts(), ["MenuItemAdded"]);
});

test("menu commands require restaurant_id and menu_item_id", async () => {
  const { as } = await createWorld();

  await assert.rejects(
    async () =>
      as(owner).createMenu(
        create(CreateMenuRequestSchema, { restaurantId: "" }),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
  );
  await assert.rejects(
    async () =>
      as(owner).setMenuItemAvailability(
        create(SetMenuItemAvailabilityRequestSchema, {
          restaurantId: "r1",
          menuItemId: "",
        }),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
  );
});
