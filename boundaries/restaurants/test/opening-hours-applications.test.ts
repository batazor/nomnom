import assert from "node:assert/strict";
import test from "node:test";

import { timestampDate } from "@bufbuild/protobuf/wkt";
import { Code } from "@connectrpc/connect";

import { PermissionDeniedError } from "../pkg/authz/errors.ts";
import { GetRestaurantHandler } from "../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import { ListRestaurantsHandler } from "../slices/restaurant/applications/list-restaurants/list-restaurants.ts";
import {
  InvalidOpeningHoursError,
  InvalidTimeZoneError,
} from "../slices/restaurant/domains/restaurant/restaurant.ts";
import { AcceptOrderHandler } from "../slices/restaurantorder/applications/accept-order/accept-order.ts";
import { GuardFailedError } from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import { OpeningHoursAcceptanceConditions } from "../slices/restaurantorder/infrastructure/acceptance/opening-hours.ts";
import { RepositoryAcceptanceFactsReader } from "../slices/restaurantorder/infrastructure/restaurant-facts/restaurant-facts.ts";
import {
  context,
  createWorld,
  rejectsWith,
  resolverOf,
} from "./support/grpc.ts";
import {
  cook,
  createKitchenWorld,
  FixedDurationReadyTimeEstimator,
  minutes,
} from "./support/kitchen.ts";
import {
  BERLIN,
  createRestaurantWorld,
  market,
  operator,
  owner,
  stranger,
} from "./support/restaurants.ts";

/** 10:00-22:00 every day of the week. */
const TEN_TO_TEN = [1, 2, 3, 4, 5, 6, 7].map((day) => ({
  day,
  opens: "10:00",
  closes: "22:00",
}));

/** Wednesday 2026-10-28 in Europe/Berlin (CET, UTC+1) at `hh:mm` local. */
const wednesday = (hh: number, mm = 0): Date =>
  new Date(Date.UTC(2026, 9, 28, hh - 1, mm));

test("AC-938 a restaurant in an unknown time zone is not registered", async () => {
  const world = await createRestaurantWorld();
  await assert.rejects(
    world.register.execute({
      subject: operator,
      name: "Pho",
      ownerId: owner.id,
      marketId: market,
      timeZone: "Mars/Olympus",
    }),
    InvalidTimeZoneError,
  );
  assert.deepEqual(await world.store.find({}), []);
  assert.deepEqual(world.authorization.owners, []);
});

test("AC-939 AC-940 Set Opening Hours replaces the schedule, an empty interval changes nothing", async () => {
  const world = await createRestaurantWorld();
  const restaurantId = await world.givenRestaurant("Pho", { open: false });
  const friday = [{ day: 5, opens: "18:00", closes: "02:00" }];

  const set = await world.setOpeningHours.execute({
    subject: owner,
    restaurantId,
    openingHours: friday,
  });
  const repeated = await world.setOpeningHours.execute({
    subject: owner,
    restaurantId,
    openingHours: friday,
  });
  await assert.rejects(
    world.setOpeningHours.execute({
      subject: owner,
      restaurantId,
      openingHours: [{ day: 1, opens: "10:00", closes: "10:00" }],
    }),
    InvalidOpeningHoursError,
  );
  await assert.rejects(
    world.setOpeningHours.execute({
      subject: stranger,
      restaurantId,
      openingHours: TEN_TO_TEN,
    }),
    PermissionDeniedError,
  );

  assert.deepEqual(set.openingHours, friday);
  assert.equal(set.changed, true);
  assert.deepEqual([repeated.changed, repeated.version], [false, set.version]);
  assert.deepEqual(
    (await world.restaurants.get(restaurantId))?.openingHours.intervals,
    friday,
  );
  assert.deepEqual(await world.outbox.records(), [], "no business fact");
});

test("AC-948 AC-946 AC-947 Get Restaurant computes acceptance and the next opening when read", async () => {
  const world = await createRestaurantWorld();
  const get = new GetRestaurantHandler({
    reader: world.catalog,
    clock: world.clock,
  });
  const scheduled = await world.givenRestaurant("Scheduled", {
    open: true,
    openingHours: TEN_TO_TEN,
  });
  const closed = await world.givenRestaurant("Closed", {
    open: true,
    openingHours: TEN_TO_TEN,
  });
  await world.close.execute({ subject: owner, restaurantId: closed });
  const unscheduled = await world.givenRestaurant("Unscheduled", {
    open: true,
    openingHours: [],
  });

  world.clock.set(wednesday(8));
  const early = await get.execute({ restaurantId: scheduled });
  assert.equal(early.acceptingOrders, false);
  assert.deepEqual(early.nextOpeningAt, wednesday(10), "AC-948 today 10:00");

  world.clock.set(wednesday(12));
  const noon = await get.execute({ restaurantId: scheduled });
  assert.equal(noon.acceptingOrders, true);
  assert.equal(noon.nextOpeningAt, undefined);

  const flagClosed = await get.execute({ restaurantId: closed });
  assert.equal(flagClosed.acceptingOrders, false, "AC-946");
  assert.equal(flagClosed.nextOpeningAt, undefined, "AC-946 no next opening");

  const empty = await get.execute({ restaurantId: unscheduled });
  assert.equal(empty.acceptingOrders, false, "AC-947");
  assert.equal(empty.nextOpeningAt, undefined);
});

test("AC-213 List Restaurants lists by default only restaurants inside their schedule", async () => {
  const world = await createRestaurantWorld();
  const list = new ListRestaurantsHandler({
    reader: world.catalog,
    clock: world.clock,
  });
  const ids = [
    await world.givenRestaurant("A", { open: true, openingHours: TEN_TO_TEN }),
    await world.givenRestaurant("B", {
      open: true,
      openingHours: [{ day: 3, opens: "18:00", closes: "23:00" }],
    }),
    await world.givenRestaurant("C", { open: true, openingHours: TEN_TO_TEN }),
  ];

  world.clock.set(wednesday(12));
  const noon = await list.execute({ marketId: market, limit: 1 });
  const rest = await list.execute({
    marketId: market,
    limit: 1,
    cursor: noon.nextCursor,
  });
  assert.deepEqual(
    [...noon.restaurants, ...rest.restaurants].map(
      (restaurant) => restaurant.restaurantId,
    ),
    [ids[0], ids[2]],
    "B opens at 18:00, the page skips it",
  );
  assert.equal(rest.nextCursor, undefined);

  world.clock.set(wednesday(22, 30));
  const night = await list.execute({ marketId: market });
  assert.deepEqual(
    night.restaurants.map((restaurant) => restaurant.restaurantId),
    [ids[1]],
  );
  const all = await list.execute({ marketId: market, includeClosed: true });
  assert.deepEqual(
    all.restaurants.map((restaurant) => [
      restaurant.restaurantId,
      restaurant.acceptingOrders,
    ]),
    [
      [ids[0], false],
      [ids[1], true],
      [ids[2], false],
    ],
  );
});

test("AC-945 accepting an order outside the schedule fails with outside_opening_hours", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  await world.setOpeningHours.execute({
    subject: owner,
    restaurantId,
    openingHours: TEN_TO_TEN,
  });
  const accept = new AcceptOrderHandler({
    accessControl: world.kitchenAccessControl,
    unitOfWork: world.unitOfWork,
    restaurantOrders: world.restaurantOrders,
    facts: new RepositoryAcceptanceFactsReader(world.menuWorld),
    conditions: new OpeningHoursAcceptanceConditions(world.restaurants),
    readyTimeEstimator: new FixedDurationReadyTimeEstimator(minutes(20)),
    outbox: world.outbox,
    clock: world.clock,
  });
  const late = await world.givenReceivedOrder(restaurantId, phoId);
  const onTime = await world.givenReceivedOrder(restaurantId, phoId);

  world.clock.set(wednesday(23));
  await assert.rejects(
    accept.execute({
      subject: cook,
      restaurantId,
      restaurantOrderId: late.restaurantOrderId,
    }),
    (error) =>
      error instanceof GuardFailedError &&
      error.reasons.some((reason) => reason.code === "outside_opening_hours"),
  );
  assert.equal(
    (await world.restaurantOrders.get(late.restaurantOrderId))?.state,
    "RECEIVED",
  );

  world.clock.set(wednesday(21, 59));
  const accepted = await accept.execute({
    subject: cook,
    restaurantId,
    restaurantOrderId: onTime.restaurantOrderId,
  });
  assert.equal(accepted.state, "QUEUED");
});

test("FR-696 FR-204 SetOpeningHours and GetRestaurant over gRPC", async () => {
  const { world, service } = await createWorld({
    subjects: resolverOf(owner),
  });
  const restaurantId = await world.givenRestaurant("Pho", {
    open: true,
    openingHours: [],
  });

  const response = await service.setOpeningHours(
    {
      restaurantId,
      openingHours: TEN_TO_TEN.map((interval) => ({
        dayOfWeek: interval.day,
        opens: interval.opens,
        closes: interval.closes,
      })),
    },
    context,
  );
  assert.equal(response.changed, true);
  assert.equal(response.restaurant?.timeZone, BERLIN);
  assert.equal(response.restaurant?.openingHours.length, 7);

  await assert.rejects(
    service.setOpeningHours(
      {
        restaurantId,
        openingHours: [
          { dayOfWeek: 5, opens: "18:00", closes: "02:00" },
          { dayOfWeek: 6, opens: "01:00", closes: "12:00" },
        ],
      },
      context,
    ),
    rejectsWith(Code.InvalidArgument),
  );

  world.clock.set(wednesday(8));
  const before = await service.getRestaurant({ restaurantId }, context);
  assert.equal(before.restaurant?.acceptingOrders, false);
  assert.deepEqual(
    before.restaurant?.nextOpeningAt === undefined
      ? undefined
      : timestampDate(before.restaurant.nextOpeningAt),
    wednesday(10),
  );
  world.clock.set(wednesday(10));
  const after = await service.getRestaurant({ restaurantId }, context);
  assert.equal(after.restaurant?.acceptingOrders, true);
  assert.equal(after.restaurant?.nextOpeningAt, undefined);
});
