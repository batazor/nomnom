import assert from "node:assert/strict";
import test from "node:test";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  AssignStaffRequestSchema,
  RegisterRestaurantRequestSchema,
  RevokeStaffRequestSchema,
} from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { AuthorizationUnavailableError } from "../pkg/application/errors.ts";
import type { Subject } from "../pkg/application/subject.ts";
import { GetRestaurantHandler } from "../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import { createService, rejectingSubjects } from "./support/grpc.ts";
import {
  createRestaurantWorld,
  operator,
  owner,
  stranger,
} from "./support/restaurants.ts";

const context = undefined as never;
const cook = "cook";

const rejectsWith = (code: Code) => (error: unknown) =>
  error instanceof ConnectError && error.code === code;

const unused = {
  execute: async (): Promise<never> => assert.fail("unexpected call"),
};

/** Admin RPCs over the MongoDB restaurant world; `as` switches the caller. */
const createWorld = async () => {
  const world = await createRestaurantWorld();
  let caller: Subject = operator;
  const service = createService({
    getRestaurant: new GetRestaurantHandler({ reader: world.catalog }),
    getMenu: unused,
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
    registerRestaurant: world.register,
    updateRestaurant: world.update,
    assignStaff: world.assignStaff,
    revokeStaff: world.revokeStaff,
    openRestaurant: unused,
    closeRestaurant: unused,
    createMenu: unused,
    addMenuItem: unused,
    updateMenuItem: unused,
    setMenuItemAvailability: unused,
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
  const register = async () => {
    const response = await as(operator).registerRestaurant(
      create(RegisterRestaurantRequestSchema, {
        name: "Pho",
        ownerId: owner.id,
        marketId: "berlin",
      }),
      context,
    );
    return response.restaurant?.restaurantId ?? "";
  };
  return { world, as, register };
};

const staffRequest = (restaurantId: string, userId = cook) => ({
  restaurantId,
  userId,
});

test("FR-446 RegisterRestaurant returns the restaurant and version", async () => {
  const { world, as } = await createWorld();

  const response = await as(operator).registerRestaurant(
    create(RegisterRestaurantRequestSchema, {
      name: "Pho",
      ownerId: owner.id,
      marketId: "berlin",
    }),
    context,
  );

  assert.notEqual(response.restaurant?.restaurantId, "");
  assert.equal(response.restaurant?.name, "Pho");
  assert.equal(response.restaurant?.isOpen, false);
  assert.equal(response.restaurant?.marketId, "berlin");
  assert.equal(response.version, 1n);
  assert.deepEqual(world.authorization.owners, [
    { restaurantId: response.restaurant?.restaurantId, ownerId: owner.id },
  ]);
});

test("RegisterRestaurant without platform#register_restaurant is PERMISSION_DENIED", async () => {
  const { world, as } = await createWorld();

  await assert.rejects(
    async () =>
      as(stranger).registerRestaurant(
        create(RegisterRestaurantRequestSchema, {
          name: "Pho",
          ownerId: owner.id,
          marketId: "berlin",
        }),
        context,
      ),
    rejectsWith(Code.PermissionDenied),
  );
  assert.equal((await world.store.find({})).length, 0);
});

test("RegisterRestaurant with missing fields is INVALID_ARGUMENT", async () => {
  const { world, as } = await createWorld();

  for (const request of [
    { name: " ", ownerId: owner.id, marketId: "berlin" },
    { name: "Pho", ownerId: "", marketId: "berlin" },
    // ADR 41: the Market is required and a slug.
    { name: "Pho", ownerId: owner.id, marketId: "" },
    { name: "Pho", ownerId: owner.id, marketId: "Berlin" },
  ]) {
    await assert.rejects(
      async () =>
        as(operator).registerRestaurant(
          create(RegisterRestaurantRequestSchema, request),
          context,
        ),
      rejectsWith(Code.InvalidArgument),
    );
  }
  assert.equal((await world.store.find({})).length, 0);
});

test("AssignStaff returns sorted staff; a repeat is changed=false", async () => {
  const { world, as, register } = await createWorld();
  const restaurantId = await register();
  await as(owner).assignStaff(
    create(AssignStaffRequestSchema, staffRequest(restaurantId, "zed")),
    context,
  );

  const assigned = await as(owner).assignStaff(
    create(AssignStaffRequestSchema, staffRequest(restaurantId)),
    context,
  );
  const repeat = await as(owner).assignStaff(
    create(AssignStaffRequestSchema, staffRequest(restaurantId)),
    context,
  );

  assert.equal(assigned.restaurantId, restaurantId);
  assert.deepEqual(assigned.staff, [cook, "zed"]);
  assert.equal(assigned.changed, true);
  assert.deepEqual(repeat.staff, [cook, "zed"]);
  assert.equal(repeat.changed, false);
  assert.deepEqual(world.authorization.staffWrites, [
    `touch:${restaurantId}:zed`,
    `touch:${restaurantId}:${cook}`,
  ]);
});

test("RevokeStaff removes the user; a repeat is changed=false", async () => {
  const { world, as, register } = await createWorld();
  const restaurantId = await register();
  await as(owner).assignStaff(
    create(AssignStaffRequestSchema, staffRequest(restaurantId)),
    context,
  );

  const revoked = await as(owner).revokeStaff(
    create(RevokeStaffRequestSchema, staffRequest(restaurantId)),
    context,
  );
  const repeat = await as(owner).revokeStaff(
    create(RevokeStaffRequestSchema, staffRequest(restaurantId)),
    context,
  );

  assert.deepEqual(revoked.staff, []);
  assert.equal(revoked.changed, true);
  assert.equal(repeat.changed, false);
  assert.equal(world.authorization.isStaff(restaurantId, cook), false);
});

test("AC-481 AssignStaff without restaurant#manage is PERMISSION_DENIED and staff is unchanged", async () => {
  const { world, as, register } = await createWorld();
  const restaurantId = await register();

  for (const call of [
    () =>
      as(stranger).assignStaff(
        create(AssignStaffRequestSchema, staffRequest(restaurantId)),
        context,
      ),
    () =>
      as(stranger).revokeStaff(
        create(RevokeStaffRequestSchema, staffRequest(restaurantId)),
        context,
      ),
  ]) {
    await assert.rejects(call, rejectsWith(Code.PermissionDenied));
  }
  assert.deepEqual((await world.restaurants.get(restaurantId))?.staff, []);
  assert.deepEqual(world.authorization.staffWrites, []);
});

test("staff RPCs map missing input to INVALID_ARGUMENT and unknown restaurants to NOT_FOUND", async () => {
  const { as, register } = await createWorld();
  const restaurantId = await register();

  await assert.rejects(
    async () =>
      as(owner).assignStaff(
        create(AssignStaffRequestSchema, staffRequest(restaurantId, " ")),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
  );
  await assert.rejects(
    async () =>
      as(owner).revokeStaff(
        create(RevokeStaffRequestSchema, staffRequest("")),
        context,
      ),
    rejectsWith(Code.InvalidArgument),
  );
  const { world } = await createWorld();
  world.authorization.grantManager(owner, "missing");
  const service = createService({
    getRestaurant: unused,
    getMenu: unused,
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
    assignStaff: world.assignStaff,
    revokeStaff: world.revokeStaff,
    openRestaurant: unused,
    closeRestaurant: unused,
    createMenu: unused,
    addMenuItem: unused,
    updateMenuItem: unused,
    setMenuItemAvailability: unused,
    getSimulator: { execute: () => Promise.reject(new Error("unused")) },
    setSimulatorEnabled: { execute: () => Promise.reject(new Error("unused")) },
    hireCook: { execute: () => Promise.reject(new Error("unused")) },
    putCookOnLine: { execute: () => Promise.reject(new Error("unused")) },
    takeCookOffLine: { execute: () => Promise.reject(new Error("unused")) },
    retireCook: { execute: () => Promise.reject(new Error("unused")) },
    getCookPool: { execute: () => Promise.reject(new Error("unused")) },
    subjects: { resolve: async () => owner },
  });
  await assert.rejects(
    async () =>
      service.assignStaff(
        create(AssignStaffRequestSchema, staffRequest("missing")),
        context,
      ),
    rejectsWith(Code.NotFound),
  );
});

test("admin RPCs map unavailable authorization to UNAVAILABLE", async () => {
  const failing = {
    execute: async (): Promise<never> => {
      throw new AuthorizationUnavailableError();
    },
  };
  const service = createService({
    getRestaurant: unused,
    getMenu: unused,
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
    registerRestaurant: failing,
    updateRestaurant: unused,
    assignStaff: failing,
    revokeStaff: failing,
    openRestaurant: failing,
    closeRestaurant: failing,
    createMenu: failing,
    addMenuItem: failing,
    updateMenuItem: failing,
    setMenuItemAvailability: failing,
    getSimulator: { execute: () => Promise.reject(new Error("unused")) },
    setSimulatorEnabled: { execute: () => Promise.reject(new Error("unused")) },
    hireCook: { execute: () => Promise.reject(new Error("unused")) },
    putCookOnLine: { execute: () => Promise.reject(new Error("unused")) },
    takeCookOffLine: { execute: () => Promise.reject(new Error("unused")) },
    retireCook: { execute: () => Promise.reject(new Error("unused")) },
    getCookPool: { execute: () => Promise.reject(new Error("unused")) },
    subjects: { resolve: async () => owner },
  });

  await assert.rejects(
    async () =>
      service.registerRestaurant(
        create(RegisterRestaurantRequestSchema, {
          name: "Pho",
          ownerId: owner.id,
          marketId: "berlin",
        }),
        context,
      ),
    rejectsWith(Code.Unavailable),
  );
  await assert.rejects(
    async () =>
      service.assignStaff(
        create(AssignStaffRequestSchema, staffRequest("r1")),
        context,
      ),
    rejectsWith(Code.Unavailable),
  );
});

test("admin RPCs are UNAUTHENTICATED for an unverified caller", async () => {
  const world = await createRestaurantWorld();
  const service = createService({
    subjects: rejectingSubjects,
    getRestaurant: unused,
    getMenu: unused,
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
    registerRestaurant: world.register,
    updateRestaurant: world.update,
    assignStaff: world.assignStaff,
    revokeStaff: world.revokeStaff,
    openRestaurant: unused,
    closeRestaurant: unused,
    createMenu: unused,
    addMenuItem: unused,
    updateMenuItem: unused,
    setMenuItemAvailability: unused,
    getSimulator: { execute: () => Promise.reject(new Error("unused")) },
    setSimulatorEnabled: { execute: () => Promise.reject(new Error("unused")) },
    hireCook: { execute: () => Promise.reject(new Error("unused")) },
    putCookOnLine: { execute: () => Promise.reject(new Error("unused")) },
    takeCookOffLine: { execute: () => Promise.reject(new Error("unused")) },
    retireCook: { execute: () => Promise.reject(new Error("unused")) },
    getCookPool: { execute: () => Promise.reject(new Error("unused")) },
  });

  await assert.rejects(
    async () =>
      service.registerRestaurant(
        create(RegisterRestaurantRequestSchema, {
          name: "Pho",
          ownerId: owner.id,
          marketId: "berlin",
        }),
        context,
      ),
    rejectsWith(Code.Unauthenticated),
  );
});
