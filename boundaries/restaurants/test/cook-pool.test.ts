import assert from "node:assert/strict";
import test from "node:test";

import {
  NotFoundError,
  PermissionDeniedError,
} from "../pkg/application/errors.ts";
import {
  CookBusyError,
  CookOnLineError,
  CookRetiredError,
  InvalidCookError,
} from "../slices/cook/domains/cook/cook.ts";
import { NoCookAvailableError } from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import { cook, createKitchenWorld, minutes } from "./support/kitchen.ts";
import { stranger } from "./support/restaurants.ts";

test("AC-775 staff hire a cook who is off line and in the Cook Pool", async () => {
  const world = await createKitchenWorld();
  const { restaurantId } = await world.givenKitchen("Pho Bar", []);

  const hired = await world.hireCook.execute({
    subject: cook,
    restaurantId,
    name: "  Aigul ",
  });

  assert.equal(hired.name, "Aigul");
  assert.equal(hired.onLine, false);
  const pool = await world.getCookPool.execute({ subject: cook, restaurantId });
  assert.deepEqual(
    pool.cooks.map((member) => [member.name, member.onLine]),
    [["Aigul", false]],
  );
});

test("AC-776 an empty or too long name hires nobody", async () => {
  const world = await createKitchenWorld();
  const { restaurantId } = await world.givenKitchen("Pho Bar", []);

  for (const name of ["   ", "x".repeat(65)]) {
    await assert.rejects(
      world.hireCook.execute({ subject: cook, restaurantId, name }),
      InvalidCookError,
    );
  }
  const pool = await world.getCookPool.execute({ subject: cook, restaurantId });
  assert.equal(pool.size, 0);
});

test("AC-777 a stranger does not change the Cook Pool", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, cookIds } = await world.givenKitchen("Pho Bar", [
    "Aigul",
  ]);
  const [cookId] = cookIds;
  assert.ok(cookId);

  await assert.rejects(
    world.hireCook.execute({ subject: stranger, restaurantId, name: "Oleg" }),
    PermissionDeniedError,
  );
  await assert.rejects(
    world.takeCookOffLine.execute({ subject: stranger, restaurantId, cookId }),
    PermissionDeniedError,
  );
  await assert.rejects(
    world.getCookPool.execute({ subject: stranger, restaurantId }),
    PermissionDeniedError,
  );
  const pool = await world.getCookPool.execute({ subject: cook, restaurantId });
  assert.deepEqual([pool.size, pool.onLine], [1, 1]);
});

test("FR-614 a cook of another restaurant and an unknown restaurant are not found", async () => {
  const world = await createKitchenWorld();
  const pho = await world.givenKitchen("Pho Bar", ["Aigul"]);
  const ramen = await world.givenKitchen("Ramen Bar", []);
  const [cookId] = pho.cookIds;
  assert.ok(cookId);

  await assert.rejects(
    world.takeCookOffLine.execute({
      subject: cook,
      restaurantId: ramen.restaurantId,
      cookId,
    }),
    NotFoundError,
  );
  world.kitchenAccessControl.grantStaff(cook, "missing");
  await assert.rejects(
    world.hireCook.execute({
      subject: cook,
      restaurantId: "missing",
      name: "Oleg",
    }),
    NotFoundError,
  );
});

test("AC-778 a cook on line gets the next order; a repeat changes nothing", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen("Pho Bar", []);
  const { cookId } = await world.hireCook.execute({
    subject: cook,
    restaurantId,
    name: "Aigul",
  });
  const order = await world.givenOrderIn("QUEUED", restaurantId, phoId);

  const first = await world.putCookOnLine.execute({
    subject: cook,
    restaurantId,
    cookId,
  });
  const repeat = await world.putCookOnLine.execute({
    subject: cook,
    restaurantId,
    cookId,
  });
  const started = await world.startNext(restaurantId);

  assert.deepEqual([first.changed, repeat.changed], [true, false]);
  assert.equal(started?.restaurantOrderId, order.restaurantOrderId);
  assert.equal(started?.state, "PREPARING");
  assert.equal(started?.cookId, cookId);
});

test("AC-779 a busy cook stays on line", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId, cookIds } = await world.givenKitchen("Pho Bar", [
    "Oleg",
  ]);
  const [cookId] = cookIds;
  assert.ok(cookId);
  const order = await world.givenOrderIn("PREPARING", restaurantId, phoId);

  await assert.rejects(
    world.takeCookOffLine.execute({ subject: cook, restaurantId, cookId }),
    CookBusyError,
  );

  const pool = await world.getCookPool.execute({ subject: cook, restaurantId });
  assert.deepEqual(
    pool.cooks.map((member) => [
      member.onLine,
      member.currentRestaurantOrderId,
    ]),
    [[true, order.restaurantOrderId]],
  );
});

test("AC-780 a cook on line cannot retire; a retired cook leaves the pool for good", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, cookIds } = await world.givenKitchen("Pho Bar", [
    "Marat",
  ]);
  const [cookId] = cookIds;
  assert.ok(cookId);
  const command = { subject: cook, restaurantId, cookId };

  await assert.rejects(world.retireCook.execute(command), CookOnLineError);
  await world.takeCookOffLine.execute(command);
  const retired = await world.retireCook.execute(command);
  const repeat = await world.retireCook.execute(command);

  assert.deepEqual([retired.changed, repeat.changed], [true, false]);
  await assert.rejects(world.putCookOnLine.execute(command), CookRetiredError);
  const pool = await world.getCookPool.execute({ subject: cook, restaurantId });
  assert.equal(pool.size, 0);
});

test("AC-781 Get Cook Pool returns the size, the line, the busy cooks and the queue", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId, cookIds } = await world.givenKitchen("Pho Bar", [
    "Aigul",
    "Oleg",
    "Marat",
  ]);
  for (const name of ["Timur", "Anna"]) {
    await world.hireCook.execute({ subject: cook, restaurantId, name });
  }
  const preparing = await world.givenOrderIn("PREPARING", restaurantId, phoId);
  const assembled = await world.givenOrderIn("ASSEMBLED", restaurantId, phoId);
  for (let queued = 0; queued < 4; queued += 1) {
    await world.givenOrderIn("QUEUED", restaurantId, phoId);
  }

  const pool = await world.getCookPool.execute({ subject: cook, restaurantId });

  assert.deepEqual(
    {
      size: pool.size,
      onLine: pool.onLine,
      busy: pool.busy,
      queued: pool.queued,
    },
    { size: 5, onLine: 3, busy: 2, queued: 4 },
  );
  assert.deepEqual(
    pool.cooks.map((member) => member.name),
    ["Aigul", "Anna", "Marat", "Oleg", "Timur"],
  );
  const working = new Set(
    pool.cooks.flatMap((member) =>
      member.currentRestaurantOrderId === undefined
        ? []
        : [member.currentRestaurantOrderId],
    ),
  );
  assert.deepEqual(
    working,
    new Set([preparing.restaurantOrderId, assembled.restaurantOrderId]),
  );
  assert.equal(cookIds.length, 3);
});

test("AC-782 without a free cook the order stays QUEUED and nothing is published", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen("Pho Bar", [
    "Aigul",
  ]);
  await world.givenOrderIn("PREPARING", restaurantId, phoId);
  const waiting = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  const outboxBefore = (await world.outbox.records()).length;

  await assert.rejects(world.startNext(restaurantId), NoCookAvailableError);

  const order = await world.restaurantOrders.get(waiting.restaurantOrderId);
  assert.equal(order?.state, "QUEUED");
  assert.equal(order?.cookId, undefined);
  assert.equal((await world.outbox.records()).length, outboxBefore);
});

test("AC-783 a never assigned cook goes first, then the earliest last assignment", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId, cookIds } = await world.givenKitchen("Pho Bar", [
    "Aigul",
    "Oleg",
  ]);
  const [aigul, oleg] = cookIds;
  assert.ok(aigul && oleg);
  const cookOf = async (restaurantOrderId: string) =>
    (await world.restaurantOrders.get(restaurantOrderId))?.cookId;

  // Both never assigned: the lower id.
  const first = await world.givenOrderIn("READY", restaurantId, phoId);
  world.clock.advance(minutes(8));
  // Aigul is free again, Oleg was never assigned.
  const second = await world.givenOrderIn("READY", restaurantId, phoId);
  world.clock.advance(minutes(2));
  // Both free: Aigul was assigned 10 minutes ago, Oleg 2 minutes ago.
  const third = await world.givenOrderIn("PREPARING", restaurantId, phoId);

  assert.deepEqual(
    [
      await cookOf(first.restaurantOrderId),
      await cookOf(second.restaurantOrderId),
      await cookOf(third.restaurantOrderId),
    ],
    [aigul, oleg, aigul],
  );
});

test("AC-784 a cook is free again once the order is READY and keeps it as history", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId, cookIds } = await world.givenKitchen("Pho Bar", [
    "Oleg",
  ]);
  const [oleg] = cookIds;
  const first = await world.givenOrderIn("ASSEMBLED", restaurantId, phoId);
  const second = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  await assert.rejects(world.startNext(restaurantId), NoCookAvailableError);

  await world.markReady.execute(first);
  const started = await world.startNext(restaurantId);

  assert.equal(started?.restaurantOrderId, second.restaurantOrderId);
  assert.equal(started?.cookId, oleg);
  const ready = await world.restaurantOrders.get(first.restaurantOrderId);
  assert.deepEqual([ready?.state, ready?.cookId], ["READY", oleg]);
});

test("AC-785 simultaneous starts on the last free cook: one starts, one waits", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId, cookIds } = await world.givenKitchen("Pho Bar", [
    "Aigul",
  ]);
  const orders = [
    await world.givenOrderIn("QUEUED", restaurantId, phoId),
    await world.givenOrderIn("QUEUED", restaurantId, phoId),
  ];

  const outcomes = await Promise.allSettled(
    orders.map(() => world.startNext(restaurantId)),
  );

  const started = outcomes.flatMap((outcome) =>
    outcome.status === "fulfilled" ? [outcome.value] : [],
  );
  const rejected = outcomes.flatMap((outcome) =>
    outcome.status === "rejected" ? [outcome.reason] : [],
  );
  assert.equal(started.length, 1);
  assert.equal(started[0]?.cookId, cookIds[0]);
  assert.equal(rejected.length, 1);
  assert.ok(rejected[0] instanceof NoCookAvailableError, String(rejected[0]));
  const states = await Promise.all(
    orders.map(
      async (order) =>
        (await world.restaurantOrders.get(order.restaurantOrderId))?.state,
    ),
  );
  assert.deepEqual(states.sort(), ["PREPARING", "QUEUED"]);
});
