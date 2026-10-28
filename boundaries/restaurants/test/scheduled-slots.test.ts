import assert from "node:assert/strict";
import test from "node:test";
import { BinaryReader } from "@bufbuild/protobuf/wire";
import {
  durationFromMs,
  timestampDate,
  timestampFromDate,
} from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";

import {
  NotFoundError,
  PermissionDeniedError,
} from "../pkg/application/errors.ts";
import { InvalidScheduledOrderSettingsError } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import { ReserveScheduledSlotHandler } from "../slices/scheduleslot/applications/reserve-scheduled-slot/reserve-scheduled-slot.ts";
import {
  type DeliveryWindow,
  InvalidDeliveryWindowError,
  InvalidDeliveryWindowReason,
  ScheduledOrdersUnavailableError,
} from "../slices/scheduleslot/domains/scheduleslot/scheduleslot.ts";
import {
  context,
  createWorld,
  rejectsWith,
  resolverOf,
} from "./support/grpc.ts";
import {
  cook,
  createKitchenWorld,
  minutes,
  ordersService,
  SLOT_HOLD_TTL_MINUTES,
} from "./support/kitchen.ts";
import { owner, stranger } from "./support/restaurants.ts";

/** 10:00-22:00 every day of the week. */
const TEN_TO_TEN = [1, 2, 3, 4, 5, 6, 7].map((day) => ({
  day,
  opens: "10:00",
  closes: "22:00",
}));

/** Wednesday 2026-10-28 in Europe/Berlin (CET, UTC+1) at `hh:mm` local. */
const wednesday = (hh: number, mm = 0): Date =>
  new Date(Date.UTC(2026, 9, 28, hh - 1, mm));

/** `hh:mm` of a window start on the Berlin wall clock of the Wednesday. */
const local = (window: DeliveryWindow): string => {
  const at = new Date(window.start.getTime() + 60 * 60_000);
  return `${String(at.getUTCHours()).padStart(2, "0")}:${String(at.getUTCMinutes()).padStart(2, "0")}`;
};

const settings = (slotCapacity: number) => ({
  slotCapacity,
  openingLeadMs: minutes(45),
  closingGraceMs: minutes(10),
});

/**
 * Kitchen world with a restaurant open 10:00-22:00 that takes scheduled
 * orders with `capacity` per window, at `now`.
 */
const slotWorld = async (capacity: number, now: Date) => {
  const world = await createKitchenWorld();
  const restaurantId = await world.givenRestaurant("Pho", {
    open: true,
    openingHours: TEN_TO_TEN,
  });
  world.kitchenAccessControl.grantFulfillment(ordersService, restaurantId);
  await world.setScheduledOrderSettings.execute({
    subject: owner,
    restaurantId,
    settings: settings(capacity),
  });
  world.clock.set(now);
  const reserve = (orderId: string, windowStart: Date) =>
    world.reserveScheduledSlot.execute({
      subject: ordersService,
      restaurantId,
      windowStart,
      orderId,
    });
  /** Windows of the Wednesday by local start, with their availability. */
  const availability = async () =>
    new Map(
      (await world.listScheduledSlots.execute({ restaurantId }))
        .filter((slot) => slot.window.start < wednesday(24))
        .map((slot) => [local(slot.window), slot.available]),
    );
  return { world, restaurantId, reserve, availability };
};

test("AC-894 AC-895 Set Scheduled Order Settings sets the capacity, a non-positive one changes nothing", async () => {
  const { world, restaurantId, reserve } = await slotWorld(4, wednesday(12));
  for (const orderId of ["o-1", "o-2", "o-3", "o-4"]) {
    assert.equal(
      (await reserve(orderId, wednesday(19))).outcome,
      "reserved",
      orderId,
    );
  }
  assert.equal(
    (await reserve("o-5", wednesday(19))).outcome,
    "slot_unavailable",
  );

  await assert.rejects(
    world.setScheduledOrderSettings.execute({
      subject: owner,
      restaurantId,
      settings: { ...settings(0) },
    }),
    InvalidScheduledOrderSettingsError,
  );
  await assert.rejects(
    world.setScheduledOrderSettings.execute({
      subject: stranger,
      restaurantId,
      settings: settings(8),
    }),
    PermissionDeniedError,
  );
  assert.equal(
    (await world.restaurants.get(restaurantId))?.scheduledOrders?.slotCapacity,
    4,
  );
});

test("AC-956 windows of a day start at opening + lead and end at closing + grace", async () => {
  const { availability } = await slotWorld(2, wednesday(10));
  const today = [...(await availability()).keys()];
  assert.equal(today[0], "10:45");
  assert.equal(today.at(-1), "22:00");
  assert.equal(today.length, 46, "10:45..22:00 every 15 minutes");
  assert.ok(!today.includes("22:15"));
});

test("AC-896 AC-897 a window is full by confirmed and live holds; an expired hold frees it", async () => {
  const { world, reserve, availability } = await slotWorld(2, wednesday(12));
  await reserve("o-confirmed", wednesday(19));
  await world.confirmScheduledSlot.execute({
    subject: ordersService,
    orderId: "o-confirmed",
  });
  await reserve("o-held", wednesday(19));
  assert.equal((await availability()).get("19:00"), false, "AC-896");

  world.clock.advance(minutes(SLOT_HOLD_TTL_MINUTES));
  assert.equal(
    (await availability()).get("19:00"),
    true,
    "AC-897 the hold expired",
  );
  assert.equal((await reserve("o-next", wednesday(19))).outcome, "reserved");
  assert.equal((await availability()).get("19:00"), false);
});

test("FR-673 no settings or not accepting orders now lists no windows", async () => {
  const { world, restaurantId, availability } = await slotWorld(
    2,
    wednesday(12),
  );
  world.clock.set(wednesday(9));
  assert.equal((await availability()).size, 0, "before opening");
  world.clock.set(wednesday(12));
  await world.close.execute({ subject: owner, restaurantId });
  assert.equal((await availability()).size, 0, "closed by the flag");
  await world.open.execute({ subject: owner, restaurantId });
  await world.setScheduledOrderSettings.execute({
    subject: owner,
    restaurantId,
    settings: undefined,
  });
  assert.equal((await availability()).size, 0, "no settings");
  await assert.rejects(
    world.listScheduledSlots.execute({ restaurantId: "missing" }),
    NotFoundError,
  );
});

test("AC-898 Reserve holds the window; a repeat by order returns the same reservation", async () => {
  const { reserve, world, restaurantId } = await slotWorld(1, wednesday(12));
  const reserved = await reserve("o-1", wednesday(19));
  assert.deepEqual(reserved, {
    outcome: "reserved",
    window: { start: wednesday(19), end: wednesday(19, 15) },
  });
  assert.deepEqual(await reserve("o-1", wednesday(19)), reserved);
  assert.deepEqual(
    await reserve("o-1", wednesday(20)),
    reserved,
    "the repeat keeps its window",
  );
  const [slot] = await world.scheduleSlots.listBetween(
    restaurantId,
    wednesday(19),
    wednesday(19),
  );
  assert.deepEqual(slot?.reservationOf("o-1"), {
    orderId: "o-1",
    status: "held",
    expiresAt: new Date(
      wednesday(12).getTime() + minutes(SLOT_HOLD_TTL_MINUTES),
    ),
  });
});

test("AC-957 FR-674 a window off the grid, too early, beyond the horizon or after closing is invalid", async () => {
  const { reserve, world, restaurantId } = await slotWorld(4, wednesday(12));
  for (const [start, reason] of [
    [wednesday(19, 5), InvalidDeliveryWindowReason.OffGrid],
    [wednesday(12, 30), InvalidDeliveryWindowReason.BeforeMinimumLead],
    [
      new Date(wednesday(19).getTime() + 48 * 3_600_000),
      InvalidDeliveryWindowReason.BeyondHorizon,
    ],
    [wednesday(22, 15), InvalidDeliveryWindowReason.OutsideOpeningHours],
    [wednesday(23), InvalidDeliveryWindowReason.OutsideOpeningHours],
  ] as const) {
    await assert.rejects(
      reserve(`o-${start.toISOString()}`, start),
      (error) =>
        error instanceof InvalidDeliveryWindowError && error.reason === reason,
      start.toISOString(),
    );
  }
  assert.deepEqual(
    await world.scheduleSlots.listBetween(
      restaurantId,
      wednesday(0),
      wednesday(24),
    ),
    [],
    "no reservation created",
  );
  assert.equal((await reserve("o-last", wednesday(22))).outcome, "reserved");
});

test("FR-674 a restaurant without settings or not accepting now takes no reservation", async () => {
  const { world, restaurantId, reserve } = await slotWorld(2, wednesday(12));
  await world.close.execute({ subject: owner, restaurantId });
  await assert.rejects(
    reserve("o-1", wednesday(19)),
    ScheduledOrdersUnavailableError,
  );
  await world.open.execute({ subject: owner, restaurantId });
  await world.setScheduledOrderSettings.execute({
    subject: owner,
    restaurantId,
    settings: undefined,
  });
  await assert.rejects(
    reserve("o-1", wednesday(19)),
    ScheduledOrdersUnavailableError,
  );
});

test("AC-899 a full window answers with the nearest available windows", async () => {
  const { world, restaurantId, reserve } = await slotWorld(
    1,
    wednesday(18, 15),
  );
  // Windows start at 19:00 (18:15 + 45 minutes); 19:00 and 19:30 are full.
  await reserve("o-1900", wednesday(19));
  await reserve("o-1930", wednesday(19, 30));
  const reserveWithTwo = new ReserveScheduledSlotHandler({
    accessControl: world.kitchenAccessControl,
    unitOfWork: world.unitOfWork,
    restaurants: world.restaurants,
    slots: world.scheduleSlots,
    windows: world.deliveryWindows,
    holdTtlMs: minutes(SLOT_HOLD_TTL_MINUTES),
    alternatives: 2,
    clock: world.clock,
  });

  const result = await reserveWithTwo.execute({
    subject: ordersService,
    restaurantId,
    windowStart: wednesday(19),
    orderId: "o-late",
  });

  assert.deepEqual(result, {
    outcome: "slot_unavailable",
    alternatives: [
      { start: wednesday(19, 15), end: wednesday(19, 30) },
      { start: wednesday(19, 45), end: wednesday(20) },
    ],
  });
  assert.equal(await world.scheduleSlots.findByOrderId("o-late"), undefined);
});

test("AC-900 concurrent reservations never exceed the capacity", async () => {
  const { reserve, world } = await slotWorld(2, wednesday(12));
  await reserve("o-first", wednesday(19));

  const results = await Promise.all(
    ["o-a", "o-b", "o-c", "o-d"].map((orderId) =>
      reserve(orderId, wednesday(19)),
    ),
  );

  assert.deepEqual(results.map((result) => result.outcome).sort(), [
    "reserved",
    "slot_unavailable",
    "slot_unavailable",
    "slot_unavailable",
  ]);
  const slot = await world.scheduleSlots.findByOrderId("o-first");
  assert.equal(slot?.activeReservations(world.clock.now()), 2);
});

test("AC-901 a confirmed reservation never expires; a repeat is a no-op", async () => {
  const { world, reserve, availability } = await slotWorld(1, wednesday(12));
  await reserve("o-1", wednesday(19));

  const confirmed = await world.confirmScheduledSlot.execute({
    subject: ordersService,
    orderId: "o-1",
  });
  const version = (await world.scheduleSlots.findByOrderId("o-1"))?.version;
  const repeated = await world.confirmScheduledSlot.execute({
    subject: ordersService,
    orderId: "o-1",
  });

  const window = { start: wednesday(19), end: wednesday(19, 15) };
  assert.deepEqual(confirmed, { outcome: "confirmed", window });
  assert.deepEqual(repeated, confirmed);
  assert.equal(
    (await world.scheduleSlots.findByOrderId("o-1"))?.version,
    version,
  );
  world.clock.advance(minutes(SLOT_HOLD_TTL_MINUTES * 3));
  assert.equal((await availability()).get("19:00"), false);
  await assert.rejects(
    world.confirmScheduledSlot.execute({
      subject: ordersService,
      orderId: "never-reserved",
    }),
    (error) =>
      error instanceof Error && "code" in error && error.code === "not_found",
  );
});

test("AC-902 an expired hold re-checks the capacity on confirmation", async () => {
  const { world, reserve, availability } = await slotWorld(1, wednesday(12));
  await reserve("o-lapsed", wednesday(19));
  await reserve("o-room", wednesday(19, 15));
  world.clock.advance(minutes(SLOT_HOLD_TTL_MINUTES + 1));
  // Another order takes the window of the expired hold, purging it.
  assert.equal((await reserve("o-other", wednesday(19))).outcome, "reserved");

  assert.deepEqual(
    await world.confirmScheduledSlot.execute({
      subject: ordersService,
      orderId: "o-lapsed",
    }),
    { outcome: "slot_unavailable" },
  );
  const slot = await world.scheduleSlots.findByOrderId("o-lapsed");
  assert.equal(slot?.reservationOf("o-lapsed"), undefined, "not created");
  assert.equal(slot?.reservationOf("o-other")?.status, "held");

  // An expired hold whose window still has room is confirmed.
  assert.deepEqual(
    await world.confirmScheduledSlot.execute({
      subject: ordersService,
      orderId: "o-room",
    }),
    {
      outcome: "confirmed",
      window: { start: wednesday(19, 15), end: wednesday(19, 30) },
    },
  );
  assert.equal((await availability()).get("19:15"), false);
});

test("AC-903 releasing twice succeeds and leaves the window unchanged", async () => {
  const { world, restaurantId, reserve, availability } = await slotWorld(
    1,
    wednesday(12),
  );
  await reserve("o-1", wednesday(19));
  assert.equal((await availability()).get("19:00"), false);

  assert.equal(
    await world.releaseScheduledSlot.execute({
      subject: ordersService,
      orderId: "o-1",
    }),
    true,
  );
  const version = (await world.scheduleSlots.get(restaurantId, wednesday(19)))
    ?.version;
  assert.equal(
    await world.releaseScheduledSlot.execute({
      subject: ordersService,
      orderId: "o-1",
    }),
    false,
  );
  assert.equal(
    (await world.scheduleSlots.get(restaurantId, wednesday(19)))?.version,
    version,
  );
  assert.equal((await availability()).get("19:00"), true);
});

test("FR-674 FR-675 only the service identity of orders reserves, confirms and releases", async () => {
  const { world, restaurantId } = await slotWorld(1, wednesday(12));
  for (const call of [
    () =>
      world.reserveScheduledSlot.execute({
        subject: cook,
        restaurantId,
        windowStart: wednesday(19),
        orderId: "o-1",
      }),
    () => world.confirmScheduledSlot.execute({ subject: cook, orderId: "o-1" }),
    () => world.releaseScheduledSlot.execute({ subject: cook, orderId: "o-1" }),
  ]) {
    await assert.rejects(call(), PermissionDeniedError);
  }
});

test("FR-672 FR-673 FR-674 slot RPCs map results and errors to their contract", async () => {
  const { world, service } = await createWorld({
    subjects: resolverOf(ordersService),
  });
  const restaurantId = await world.givenRestaurant("Pho", {
    open: true,
    openingHours: TEN_TO_TEN,
  });
  world.kitchenAccessControl.grantFulfillment(ordersService, restaurantId);
  world.clock.set(wednesday(12));
  const reserve = (orderId: string, start: Date) =>
    service.reserveScheduledSlot(
      { restaurantId, orderId, windowStart: timestampFromDate(start) },
      context,
    );

  // orders tells this refusal apart by its google.rpc.ErrorInfo reason.
  await assert.rejects(reserve("o-1", wednesday(19)), (error) => {
    assert.ok(rejectsWith(Code.FailedPrecondition)(error));
    assert.ok(error instanceof ConnectError);
    const [detail] = error.details;
    assert.ok(detail !== undefined && "type" in detail);
    assert.equal(detail.type, "google.rpc.ErrorInfo");
    const reader = new BinaryReader(detail.value);
    const fields = new Map<number, string>();
    while (reader.pos < reader.len) {
      const [field] = reader.tag();
      fields.set(field, reader.string());
    }
    assert.deepEqual(
      [...fields],
      [
        [1, "SCHEDULED_ORDERS_UNAVAILABLE"],
        [2, "nomnom.restaurants.v1"],
      ],
    );
    return true;
  });
  await world.setScheduledOrderSettings.execute({
    subject: owner,
    restaurantId,
    settings: settings(1),
  });
  const reserved = await reserve("o-1", wednesday(19));
  assert.equal(reserved.result.case, "reserved");
  assert.deepEqual(
    reserved.result.case === "reserved" && reserved.result.value.start
      ? timestampDate(reserved.result.value.start)
      : undefined,
    wednesday(19),
  );
  const full = await reserve("o-2", wednesday(19));
  assert.equal(full.result.case, "slotUnavailable");
  assert.equal(
    full.result.case === "slotUnavailable"
      ? full.result.value.alternatives.length
      : 0,
    3,
  );
  await assert.rejects(
    reserve("o-3", wednesday(19, 5)),
    rejectsWith(Code.InvalidArgument),
  );
  await assert.rejects(
    service.reserveScheduledSlot({ restaurantId, orderId: "o-4" }, context),
    rejectsWith(Code.InvalidArgument),
  );

  const confirmed = await service.confirmScheduledSlot(
    { orderId: "o-1" },
    context,
  );
  assert.equal(confirmed.result.case, "confirmed");
  await service.releaseScheduledSlot({ orderId: "o-1" }, context);
  await service.releaseScheduledSlot({ orderId: "o-1" }, context);

  const slots = await service.listScheduledSlots({ restaurantId }, context);
  assert.equal(slots.slots[0]?.available, true);
  assert.deepEqual(
    slots.slots[0]?.window?.start === undefined
      ? undefined
      : timestampDate(slots.slots[0].window.start),
    wednesday(12, 45),
  );

  const denied = await createWorld({ subjects: resolverOf(cook) });
  await assert.rejects(
    denied.service.releaseScheduledSlot({ orderId: "o-1" }, context),
    rejectsWith(Code.PermissionDenied),
  );
});

test("FR-672 SetScheduledOrderSettings sets or clears the settings, the catalog carries them with the baseline", async () => {
  const { world, service } = await createWorld({
    subjects: resolverOf(owner),
  });
  const restaurantId = await world.givenRestaurant("Pho", {
    open: true,
    openingHours: TEN_TO_TEN,
  });

  const set = await service.setScheduledOrderSettings(
    {
      restaurantId,
      settings: {
        slotCapacity: 4,
        openingLead: durationFromMs(minutes(45)),
        closingGrace: durationFromMs(minutes(10)),
      },
    },
    context,
  );
  assert.equal(set.changed, true);
  assert.equal(set.restaurant?.scheduledOrders?.slotCapacity, 4);
  assert.equal(set.restaurant?.scheduledOrders?.openingLead?.seconds, 2700n);

  const restaurant = await service.getRestaurant({ restaurantId }, context);
  assert.equal(restaurant.restaurant?.scheduledOrders?.slotCapacity, 4);
  assert.equal(
    restaurant.restaurant?.baselinePreparation?.seconds,
    1200n,
    "the preparation time until the sample is full",
  );

  for (const request of [
    { restaurantId },
    {
      restaurantId,
      clear: true,
      settings: {
        slotCapacity: 4,
        openingLead: durationFromMs(0),
        closingGrace: durationFromMs(0),
      },
    },
    {
      restaurantId,
      settings: {
        slotCapacity: 0,
        openingLead: durationFromMs(0),
        closingGrace: durationFromMs(0),
      },
    },
    { restaurantId, settings: { slotCapacity: 4 } },
  ]) {
    await assert.rejects(
      service.setScheduledOrderSettings(request, context),
      rejectsWith(Code.InvalidArgument),
      JSON.stringify(request, (_key, value) =>
        typeof value === "bigint" ? String(value) : value,
      ),
    );
  }

  const cleared = await service.setScheduledOrderSettings(
    { restaurantId, clear: true },
    context,
  );
  assert.equal(cleared.changed, true);
  assert.equal(cleared.restaurant?.scheduledOrders, undefined);
});
