import assert from "node:assert/strict";
import test from "node:test";

import { PermissionDeniedError } from "../pkg/application/errors.ts";
import type { Subject } from "../pkg/application/subject.ts";
import { loadSimulatorConfig } from "../pkg/config/config.ts";
import { GetSimulatorHandler } from "../slices/simulator/applications/get-simulator/get-simulator.ts";
import { SetSimulatorEnabledHandler } from "../slices/simulator/applications/set-simulator-enabled/set-simulator-enabled.ts";
import {
  KitchenSimulator,
  SIMULATED_FAILURE_REASON,
  SIMULATED_REJECTION_REASON,
} from "../slices/simulator/applications/simulate-kitchen/simulate-kitchen.ts";
import { chooseKitchenAction } from "../slices/simulator/domains/simulator/simulator.ts";
import { MongoSimulatorState } from "../slices/simulator/infrastructure/mongodb/simulator-state.ts";
import { RepositoryRestaurantOwners } from "../slices/simulator/infrastructure/restaurant-owners.ts";
import { createKitchenWorld } from "./support/kitchen.ts";
import { createTestDatabase } from "./support/mongodb.ts";
import { FixedClock, owner, startOfDay } from "./support/restaurants.ts";

const operator: Subject = { type: "user", id: "operator" };

const validEnvironment = {
  SIMULATOR__INTERVAL: "5s",
  SIMULATOR__MIN_DELAY: "30s",
  SIMULATOR__MAX_DELAY: "2m",
};

test("FR-606 reads the required Simulator settings", () => {
  assert.deepEqual(loadSimulatorConfig(validEnvironment), {
    intervalMs: 5_000,
    minDelayMs: 30_000,
    maxDelayMs: 120_000,
  });
});

test("AC-765 MIN_DELAY longer than MAX_DELAY stops startup", () => {
  assert.throws(
    () =>
      loadSimulatorConfig({
        ...validEnvironment,
        SIMULATOR__MIN_DELAY: "3m",
        SIMULATOR__MAX_DELAY: "1m",
      }),
    /SIMULATOR__MIN_DELAY must not be longer than SIMULATOR__MAX_DELAY/,
  );
});

test("FR-606 an empty or invalid Simulator setting stops startup", () => {
  for (const name of Object.keys(validEnvironment)) {
    for (const value of [undefined, "", "  ", "5", "soon"]) {
      assert.throws(
        () => loadSimulatorConfig({ ...validEnvironment, [name]: value }),
        new RegExp(name),
        `${name}=${value}`,
      );
    }
  }
  assert.throws(
    () =>
      loadSimulatorConfig({ ...validEnvironment, SIMULATOR__INTERVAL: "0s" }),
    /SIMULATOR__INTERVAL must be positive/,
  );
});

test("FR-605 outcomes follow the weights of each state", () => {
  assert.equal(chooseKitchenAction("RECEIVED", 0), "accept");
  assert.equal(chooseKitchenAction("RECEIVED", 0.899), "accept");
  assert.equal(chooseKitchenAction("RECEIVED", 0.9), "reject");
  assert.equal(chooseKitchenAction("QUEUED", 0.89), "start_preparation");
  assert.equal(chooseKitchenAction("QUEUED", 0.95), "report_delay");
  assert.equal(chooseKitchenAction("PREPARING", 0.91), "mark_assembled");
  assert.equal(chooseKitchenAction("PREPARING", 0.93), "report_failure");
  assert.equal(chooseKitchenAction("PREPARING", 0.98), "report_delay");
  assert.equal(chooseKitchenAction("ASSEMBLED", 0.99), "mark_ready");
});

/** Operator use cases over the MongoDB state with a test access graph. */
const simulatorUseCases = (state: MongoSimulatorState) => {
  const accessControl = {
    canOperateSimulator: async (subject: Subject) => subject.id === operator.id,
  };
  const clock = new FixedClock(startOfDay);
  return {
    get: new GetSimulatorHandler({ accessControl, state }),
    set: new SetSimulatorEnabledHandler({ accessControl, state, clock }),
  };
};

test("AC-761 the enabled state survives a restart; a new service is disabled", async () => {
  const { db } = await createTestDatabase();
  const first = simulatorUseCases(new MongoSimulatorState(db));
  assert.deepEqual(await first.get.execute({ subject: operator }), {
    enabled: false,
  });

  assert.deepEqual(
    await first.set.execute({ subject: operator, enabled: true }),
    { enabled: true },
  );

  const restarted = simulatorUseCases(new MongoSimulatorState(db));
  assert.deepEqual(await restarted.get.execute({ subject: operator }), {
    enabled: true,
  });
  await restarted.set.execute({ subject: operator, enabled: false });
  assert.deepEqual(await restarted.get.execute({ subject: operator }), {
    enabled: false,
  });
});

test("AC-762 a non-operator gets PERMISSION_DENIED and the state does not change", async () => {
  const { db } = await createTestDatabase();
  const state = new MongoSimulatorState(db);
  const useCases = simulatorUseCases(state);

  await assert.rejects(
    useCases.get.execute({ subject: owner }),
    PermissionDeniedError,
  );
  await assert.rejects(
    useCases.set.execute({ subject: owner, enabled: true }),
    PermissionDeniedError,
  );
  assert.equal(await state.isEnabled(), false);
});

/** Random numbers in the given order; running out fails the test. */
const sequence =
  (...values: number[]) =>
  () => {
    const value = values.shift();
    if (value === undefined) throw new Error("random sequence exhausted");
    return value;
  };

const DELAY = { minMs: 1_000, maxMs: 2_000 };

const createSimulatorWorld = async (random: () => number) => {
  const world = await createKitchenWorld();
  const state = new MongoSimulatorState(world.db);
  const logged: Record<string, unknown>[] = [];
  const simulator = new KitchenSimulator({
    state,
    orders: world.board,
    owners: new RepositoryRestaurantOwners(world.restaurants),
    commands: {
      accept: world.accept,
      reject: world.reject,
      startPreparation: world.startPreparation,
      markAssembled: world.markAssembled,
      markReady: world.markReady,
      reportDelay: world.reportDelay,
      reportFailure: world.reportFailure,
    },
    clock: world.clock,
    random,
    delay: DELAY,
    log: (_message, fields) => logged.push(fields ?? {}),
  });
  /** Open kitchen whose owner is kitchen staff (operate_kitchen). */
  const givenOwnedKitchen = async (name?: string) => {
    const kitchen = await world.givenKitchen(name);
    world.kitchenAccessControl.grantStaff(owner, kitchen.restaurantId);
    return kitchen;
  };
  const stateOf = async (restaurantId: string, restaurantOrderId: string) =>
    (await world.board.get(restaurantId, restaurantOrderId))?.state;
  return { ...world, state, simulator, logged, givenOwnedKitchen, stateOf };
};

test("AC-764 a disabled Simulator changes nothing", async () => {
  const world = await createSimulatorWorld(sequence());
  const { restaurantId, phoId } = await world.givenOwnedKitchen();
  const order = await world.givenReceivedOrder(restaurantId, phoId);

  for (let pass = 0; pass < 3; pass += 1) {
    await world.simulator.tick();
    world.clock.advance(DELAY.maxMs);
  }

  assert.equal(
    await world.stateOf(restaurantId, order.restaurantOrderId),
    "RECEIVED",
  );
  assert.equal(world.simulator.scheduled, 0);
});

test("AC-763 an incoming order of every restaurant is accepted or rejected by its owner after the delay", async () => {
  // Delay of order 1, delay of order 2, then outcomes: accept, reject.
  const world = await createSimulatorWorld(sequence(0, 1 - 1e-9, 0, 0.95));
  const pho = await world.givenOwnedKitchen("Pho Bar");
  const ramen = await world.givenOwnedKitchen("Ramen Bar");
  const first = await world.givenReceivedOrder(pho.restaurantId, pho.phoId);
  const second = await world.givenReceivedOrder(
    ramen.restaurantId,
    ramen.phoId,
  );
  await world.state.setEnabled(true, world.clock.now());

  await world.simulator.tick();
  assert.equal(world.simulator.scheduled, 2, "both orders are scheduled");
  world.clock.advance(DELAY.minMs - 1);
  await world.simulator.tick();
  assert.equal(
    await world.stateOf(pho.restaurantId, first.restaurantOrderId),
    "RECEIVED",
    "nothing happens before MIN_DELAY",
  );

  world.clock.advance(DELAY.maxMs);
  await world.simulator.tick();

  assert.equal(
    await world.stateOf(pho.restaurantId, first.restaurantOrderId),
    "QUEUED",
  );
  const rejected = await world.board.get(
    ramen.restaurantId,
    second.restaurantOrderId,
  );
  assert.equal(rejected?.state, "REJECTED");
  assert.equal(rejected?.rejectionReason, SIMULATED_REJECTION_REASON);
  assert.deepEqual(world.logged, []);
});

test("FR-605 an order walks QUEUED → PREPARING → ASSEMBLED → READY through the real handlers", async () => {
  // Per state: schedule delay 0, outcome 0.
  const world = await createSimulatorWorld(sequence(0, 0, 0, 0, 0, 0));
  const { restaurantId, phoId } = await world.givenOwnedKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  await world.state.setEnabled(true, world.clock.now());

  for (const expected of ["PREPARING", "ASSEMBLED", "READY"]) {
    await world.simulator.tick();
    world.clock.advance(DELAY.minMs);
    await world.simulator.tick();
    assert.equal(
      await world.stateOf(restaurantId, order.restaurantOrderId),
      expected,
    );
  }
});

test("FR-605 delay moves expected_ready_at later and failure fails the order", async () => {
  // Orders come sorted by state (PREPARING before QUEUED): schedule delays
  // 0 and 0, then PREPARING → failure (0.93), QUEUED → delay (0.95) by 0.5.
  const world = await createSimulatorWorld(sequence(0, 0, 0.93, 0.95, 0.5));
  const { restaurantId, phoId } = await world.givenOwnedKitchen();
  const queued = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const preparing = await world.givenOrderIn("PREPARING", restaurantId, phoId);
  const before = await world.board.get(restaurantId, queued.restaurantOrderId);
  await world.state.setEnabled(true, world.clock.now());

  await world.simulator.tick();
  world.clock.advance(DELAY.minMs);
  await world.simulator.tick();

  const delayed = await world.board.get(restaurantId, queued.restaurantOrderId);
  assert.equal(delayed?.state, "QUEUED");
  assert.equal(
    delayed?.timeline.expectedReadyAt?.getTime(),
    (before?.timeline.expectedReadyAt?.getTime() ?? Number.NaN) + 1_500,
  );
  const failed = await world.board.get(
    restaurantId,
    preparing.restaurantOrderId,
  );
  assert.equal(failed?.state, "FAILED");
  assert.equal(failed?.failureReason, SIMULATED_FAILURE_REASON);
});

test("FR-605 a failed use case is logged, dropped and does not stop the pass", async () => {
  // Order of the stranger kitchen first (sorted by receivedAt): denied.
  const world = await createSimulatorWorld(sequence(0, 0, 0, 0));
  const denied = await world.givenKitchen("No Owner Access");
  const allowed = await world.givenOwnedKitchen("Pho Bar");
  const deniedOrder = await world.givenReceivedOrder(
    denied.restaurantId,
    denied.phoId,
  );
  const allowedOrder = await world.givenReceivedOrder(
    allowed.restaurantId,
    allowed.phoId,
  );
  await world.state.setEnabled(true, world.clock.now());

  await world.simulator.tick();
  world.clock.advance(DELAY.minMs);
  await world.simulator.tick();

  assert.equal(
    await world.stateOf(denied.restaurantId, deniedOrder.restaurantOrderId),
    "RECEIVED",
  );
  assert.equal(
    await world.stateOf(allowed.restaurantId, allowedOrder.restaurantOrderId),
    "QUEUED",
  );
  assert.equal(world.logged.length, 1);
  assert.equal(world.logged[0]?.action, "accept");
  assert.ok(world.logged[0]?.error instanceof PermissionDeniedError);
  assert.equal(world.simulator.scheduled, 0, "the failed entry is dropped");
});

test("AC-764 disabling forgets the schedule", async () => {
  const world = await createSimulatorWorld(sequence(0));
  const { restaurantId, phoId } = await world.givenOwnedKitchen();
  const order = await world.givenReceivedOrder(restaurantId, phoId);
  await world.state.setEnabled(true, world.clock.now());
  await world.simulator.tick();
  assert.equal(world.simulator.scheduled, 1);

  await world.state.setEnabled(false, world.clock.now());
  world.clock.advance(DELAY.maxMs);
  await world.simulator.tick();

  assert.equal(world.simulator.scheduled, 0);
  assert.equal(
    await world.stateOf(restaurantId, order.restaurantOrderId),
    "RECEIVED",
  );
});
