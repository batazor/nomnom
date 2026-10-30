import assert from "node:assert/strict";
import test from "node:test";

import { PermissionDeniedError } from "../pkg/authz/errors.ts";
import type { Subject } from "../pkg/authz/subject.ts";
import { loadSimulatorConfig } from "../pkg/config/config.ts";
import { GetSimulatorHandler } from "../slices/simulator/applications/get-simulator/get-simulator.ts";
import { SetSimulatorEnabledHandler } from "../slices/simulator/applications/set-simulator-enabled/set-simulator-enabled.ts";
import {
  KitchenSimulator,
  SIMULATED_FAILURE_REASON,
  SIMULATED_REJECTION_REASON,
} from "../slices/simulator/applications/simulate-kitchen/simulate-kitchen.ts";
import { chooseKitchenAction } from "../slices/simulator/domains/simulator/simulator.ts";
import { DatabaseSimulatorState } from "../slices/simulator/infrastructure/repository/simulator-state.ts";
import { RepositoryRestaurantOwners } from "../slices/simulator/infrastructure/restaurant-owners.ts";
import { createTestDatabase } from "./support/database.ts";
import {
  createKitchenWorld,
  minutes,
  PREPARATION_MINUTES,
} from "./support/kitchen.ts";
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
const simulatorUseCases = (state: DatabaseSimulatorState) => {
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
  const first = simulatorUseCases(new DatabaseSimulatorState(db));
  assert.deepEqual(await first.get.execute({ subject: operator }), {
    enabled: false,
  });

  assert.deepEqual(
    await first.set.execute({ subject: operator, enabled: true }),
    { enabled: true },
  );

  const restarted = simulatorUseCases(new DatabaseSimulatorState(db));
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
  const state = new DatabaseSimulatorState(db);
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
  const state = new DatabaseSimulatorState(world.db);
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
  const givenOwnedKitchen = async (name?: string, cooks?: string[]) => {
    const kitchen = await world.givenKitchen(name, cooks);
    world.kitchenAccessControl.grantStaff(owner, kitchen.restaurantId);
    return kitchen;
  };
  const stateOf = async (restaurantId: string, restaurantOrderId: string) =>
    (await world.board.get(restaurantId, restaurantOrderId))?.state;
  return { ...world, state, simulator, logged, givenOwnedKitchen, stateOf };
};

test("ADR 41 the Simulator keeps the Market of every order and names none itself", async () => {
  // Delays of both orders, then two accept outcomes with their step delays.
  const world = await createSimulatorWorld(
    sequence(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0),
  );
  const berlin = await world.givenOwnedKitchen("Berlin Pho");
  const hamburgKitchen = await world.givenKitchen(
    "Fischbude",
    ["Jan"],
    "hamburg",
  );
  world.kitchenAccessControl.grantStaff(owner, hamburgKitchen.restaurantId);
  const berlinOrder = await world.givenReceivedOrder(
    berlin.restaurantId,
    berlin.phoId,
  );
  const hamburgOrder = await world.givenReceivedOrder(
    hamburgKitchen.restaurantId,
    hamburgKitchen.phoId,
  );

  assert.deepEqual(
    (await world.board.listPendingKitchenOrders()).map((order) => [
      order.restaurantOrderId,
      order.marketId,
    ]),
    [
      [berlinOrder.restaurantOrderId, "berlin"],
      [hamburgOrder.restaurantOrderId, "hamburg"],
    ],
  );
  await world.state.setEnabled(true, world.clock.now());
  await world.simulator.tick();
  world.clock.advance(DELAY.minMs);
  await world.simulator.tick();

  const accepted = (await world.outbox.records())
    .map(
      (record) =>
        record.event as unknown as Readonly<{
          type: string;
          restaurantOrderId: string;
          marketId: string;
        }>,
    )
    .filter((event) => event.type === "RestaurantOrderAccepted");
  assert.deepEqual(
    accepted.map((event) => [event.restaurantOrderId, event.marketId]).sort(),
    [
      [berlinOrder.restaurantOrderId, "berlin"],
      [hamburgOrder.restaurantOrderId, "hamburg"],
    ].sort(),
  );
});

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
  // Delay of order 1, delay of order 2, then outcome accept with its three
  // planned step delays, outcome reject.
  const world = await createSimulatorWorld(
    sequence(0, 1 - 1e-9, 0, 0, 0, 0, 0.95),
  );
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

test("FR-605 an order without a plan walks QUEUED → PREPARING → ASSEMBLED → READY through the real handlers", async () => {
  // A plan from now (three step delays 0), then outcome 0 per state. The
  // planned READY is before expected_ready_at, so no delay is reported.
  const world = await createSimulatorWorld(sequence(0, 0, 0, 0, 0, 0));
  const { restaurantId, phoId } = await world.givenOwnedKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const before = await world.board.get(restaurantId, order.restaurantOrderId);
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
  const ready = await world.board.get(restaurantId, order.restaurantOrderId);
  assert.deepEqual(
    ready?.timeline.expectedReadyAt,
    before?.timeline.expectedReadyAt,
  );
  assert.ok(!(await world.outbox.eventTypes()).includes("PreparationDelayed"));
});

test("FR-605 delay moves expected_ready_at later and failure fails the order", async () => {
  // Orders come sorted by state (PREPARING before QUEUED): plans from now
  // with step delays 0 (two and three steps), then PREPARING → failure
  // (0.93), QUEUED → delay (0.95) by 0.5.
  const world = await createSimulatorWorld(
    sequence(0, 0, 0, 0, 0, 0.93, 0.95, 0.5),
  );
  const { restaurantId, phoId } = await world.givenOwnedKitchen();
  const preparing = await world.givenOrderIn("PREPARING", restaurantId, phoId);
  const queued = await world.givenOrderIn("QUEUED", restaurantId, phoId);
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
  // Decision delays 0 and 0, then per order outcome accept and three step
  // delays.
  const world = await createSimulatorWorld(
    sequence(0, 0, 0, 0, 0, 0, 0, 0, 0, 0),
  );
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
  assert.equal(
    world.simulator.planOf(deniedOrder.restaurantOrderId),
    undefined,
    "the failed entry is dropped",
  );
  assert.equal(world.simulator.scheduled, 1, "only the accepted order's plan");
});

test("FR-609 accepting plans the whole Preparation, passes the planned READY and acts at the planned moments", async () => {
  // Decision delay 0, outcome accept, step delays 1500 / 1000 / 2000, then
  // outcome 0 for each step.
  const world = await createSimulatorWorld(
    sequence(0, 0, 0.5, 0, 1 - 1e-9, 0, 0, 0),
  );
  const { restaurantId, phoId } = await world.givenOwnedKitchen();
  const order = await world.givenReceivedOrder(restaurantId, phoId);
  await world.state.setEnabled(true, world.clock.now());

  await world.simulator.tick();
  world.clock.advance(DELAY.minMs);
  const acceptedAt = world.clock.now().getTime();
  const outboxBefore = (await world.outbox.records()).length;
  await world.simulator.tick();

  const readyAt = new Date(acceptedAt + 4_500);
  const accepted = await world.board.get(restaurantId, order.restaurantOrderId);
  assert.equal(accepted?.state, "QUEUED");
  assert.deepEqual(accepted?.timeline.expectedReadyAt, readyAt);
  const [fact] = (await world.outbox.records()).slice(outboxBefore);
  assert.ok(fact !== undefined);
  assert.equal(fact.event.type, "RestaurantOrderAccepted");
  assert.deepEqual(
    (fact.event as { expectedReadyAt?: Date }).expectedReadyAt,
    readyAt,
  );
  assert.deepEqual(world.simulator.planOf(order.restaurantOrderId), {
    steps: [
      { state: "QUEUED", dueAt: acceptedAt + 1_500 },
      { state: "PREPARING", dueAt: acceptedAt + 2_500 },
      { state: "ASSEMBLED", dueAt: acceptedAt + 4_500 },
    ],
  });

  for (const [dueAt, from, to] of [
    [acceptedAt + 1_500, "QUEUED", "PREPARING"],
    [acceptedAt + 2_500, "PREPARING", "ASSEMBLED"],
    [acceptedAt + 4_500, "ASSEMBLED", "READY"],
  ] as const) {
    world.clock.set(new Date(dueAt - 1));
    await world.simulator.tick();
    assert.equal(
      await world.stateOf(restaurantId, order.restaurantOrderId),
      from,
      `nothing happens before ${from} is due`,
    );
    world.clock.set(new Date(dueAt));
    await world.simulator.tick();
    assert.equal(
      await world.stateOf(restaurantId, order.restaurantOrderId),
      to,
    );
  }
  const ready = await world.board.get(restaurantId, order.restaurantOrderId);
  assert.deepEqual(ready?.timeline.readyAt, readyAt);
  assert.deepEqual(world.logged, []);
});

test("FR-609 a delay moves expected_ready_at and shifts the remaining plan to it", async () => {
  // Decision delay 0, outcome accept, step delays 1500 / 1000 / 2000; at the
  // QUEUED step outcome delay (0.95) by 1500; then outcome 0 per step.
  const world = await createSimulatorWorld(
    sequence(0, 0, 0.5, 0, 1 - 1e-9, 0.95, 0.5, 0, 0, 0),
  );
  const { restaurantId, phoId } = await world.givenOwnedKitchen();
  const order = await world.givenReceivedOrder(restaurantId, phoId);
  await world.state.setEnabled(true, world.clock.now());
  await world.simulator.tick();
  world.clock.advance(DELAY.minMs);
  const acceptedAt = world.clock.now().getTime();
  await world.simulator.tick();

  world.clock.set(new Date(acceptedAt + 1_500));
  await world.simulator.tick();

  const delayed = await world.board.get(restaurantId, order.restaurantOrderId);
  assert.equal(delayed?.state, "QUEUED");
  assert.deepEqual(
    delayed?.timeline.expectedReadyAt,
    new Date(acceptedAt + 6_000),
  );
  assert.deepEqual(world.simulator.planOf(order.restaurantOrderId), {
    steps: [
      { state: "QUEUED", dueAt: acceptedAt + 3_000 },
      { state: "PREPARING", dueAt: acceptedAt + 4_000 },
      { state: "ASSEMBLED", dueAt: acceptedAt + 6_000 },
    ],
  });
  for (const [dueAt, to] of [
    [acceptedAt + 3_000, "PREPARING"],
    [acceptedAt + 4_000, "ASSEMBLED"],
    [acceptedAt + 6_000, "READY"],
  ] as const) {
    world.clock.set(new Date(dueAt));
    await world.simulator.tick();
    assert.equal(
      await world.stateOf(restaurantId, order.restaurantOrderId),
      to,
    );
  }
  const ready = await world.board.get(restaurantId, order.restaurantOrderId);
  assert.deepEqual(ready?.timeline.readyAt, new Date(acceptedAt + 6_000));
});

test("FR-609 an order without a plan whose planned READY is late reports a delay to it", async () => {
  // After a restart: planned from now with step delays 0, READY at now + 3s
  // is later than expected_ready_at.
  const world = await createSimulatorWorld(sequence(0, 0, 0));
  const { restaurantId, phoId } = await world.givenOwnedKitchen();
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  world.clock.advance(minutes(PREPARATION_MINUTES));
  await world.state.setEnabled(true, world.clock.now());
  const now = world.clock.now().getTime();

  await world.simulator.tick();

  const delayed = await world.board.get(restaurantId, order.restaurantOrderId);
  assert.equal(delayed?.state, "QUEUED");
  assert.deepEqual(delayed?.timeline.expectedReadyAt, new Date(now + 3_000));
  assert.equal((await world.outbox.eventTypes()).at(-1), "PreparationDelayed");
  assert.deepEqual(world.simulator.planOf(order.restaurantOrderId), {
    steps: [
      { state: "QUEUED", dueAt: now + 1_000 },
      { state: "PREPARING", dueAt: now + 2_000 },
      { state: "ASSEMBLED", dueAt: now + 3_000 },
    ],
  });
  assert.deepEqual(world.logged, []);
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

test("AC-786 Preparation waits for a free cook on line and follows the actual start", async () => {
  // A plan from now (three step delays 0 → 1000 ms each), then outcome 0
  // (start preparation) on each due pass.
  const world = await createSimulatorWorld(sequence(0, 0, 0, 0, 0));
  const { restaurantId, phoId } = await world.givenOwnedKitchen("Pho Bar", []);
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  await world.state.setEnabled(true, world.clock.now());
  const plannedAt = world.clock.now().getTime();

  await world.simulator.tick();
  world.clock.advance(DELAY.minMs);
  await world.simulator.tick();

  assert.equal(
    await world.stateOf(restaurantId, order.restaurantOrderId),
    "QUEUED",
    "no cook on line: the order stays in the queue",
  );
  assert.deepEqual(world.logged, [], "a missing cook is not an error");

  world.clock.advance(5_000);
  const [cookId] = await world.givenCooksOnLine(restaurantId, ["Aigul"]);
  await world.simulator.tick();

  const started = await world.board.get(restaurantId, order.restaurantOrderId);
  assert.equal(started?.state, "PREPARING");
  assert.equal(started?.cookId, cookId);
  // The remaining steps move by the 5 s the start was late.
  assert.deepEqual(world.simulator.planOf(order.restaurantOrderId), {
    steps: [
      { state: "PREPARING", dueAt: plannedAt + 2_000 + 5_000 },
      { state: "ASSEMBLED", dueAt: plannedAt + 3_000 + 5_000 },
    ],
  });
  assert.deepEqual(world.logged, []);
});
