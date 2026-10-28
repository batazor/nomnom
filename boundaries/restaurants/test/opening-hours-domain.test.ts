import assert from "node:assert/strict";
import test from "node:test";

import {
  InvalidOpeningHoursError,
  InvalidOpeningHoursReason,
  InvalidScheduledOrderSettingsError,
  InvalidTimeZoneError,
  OpeningHours,
  Restaurant,
  TimeZone,
} from "../slices/restaurant/domains/restaurant/restaurant.ts";

const BERLIN = TimeZone.of("Europe/Berlin");
const HOUR = 3_600_000;
const MINUTE = 60_000;

/** ISO days of the week of the tests. */
const MONDAY = 1;
const FRIDAY = 5;
const SATURDAY = 6;
const SUNDAY = 7;

const utc = (iso: string): Date => new Date(iso);

const register = (timeZone = "Europe/Berlin") =>
  Restaurant.register({
    id: "r-1",
    name: "Pho",
    ownerId: "owner",
    marketId: "berlin",
    timeZone,
    registeredAt: utc("2026-10-25T09:00:00Z"),
  });

test("AC-202 AC-938 a restaurant is registered in a known IANA zone with an empty schedule", () => {
  const restaurant = register();
  assert.equal(restaurant.timeZone.value, "Europe/Berlin");
  assert.deepEqual(restaurant.openingHours.intervals, []);
  for (const zone of [
    "Mars/Olympus",
    "",
    " Europe/Berlin",
    "Europe/Atlantis",
  ]) {
    assert.throws(() => register(zone), InvalidTimeZoneError, zone);
  }
});

test("AC-939 a Friday interval 18:00-02:00 runs until Saturday 02:00", () => {
  const hours = OpeningHours.of([
    { day: FRIDAY, opens: "18:00", closes: "02:00" },
  ]);
  // Friday 2026-10-30 is in CET (UTC+1).
  assert.deepEqual(
    hours.periodAt(BERLIN, utc("2026-10-31T00:30:00Z")),
    {
      opensAt: utc("2026-10-30T17:00:00Z"),
      closesAt: utc("2026-10-31T01:00:00Z"),
    },
    "Saturday 01:30 local is inside the Friday night",
  );
  assert.equal(hours.periodAt(BERLIN, utc("2026-10-31T01:00:00Z")), undefined);
  assert.equal(hours.periodAt(BERLIN, utc("2026-10-30T16:59:00Z")), undefined);
});

test("AC-940 an empty interval 10:00-10:00 is invalid", () => {
  assert.throws(
    () => OpeningHours.of([{ day: MONDAY, opens: "10:00", closes: "10:00" }]),
    (error) =>
      error instanceof InvalidOpeningHoursError &&
      error.reason === InvalidOpeningHoursReason.EmptyInterval,
  );
  const restaurant = register();
  restaurant.setOpeningHours([
    { day: MONDAY, opens: "10:00", closes: "22:00" },
  ]);
  assert.throws(() =>
    restaurant.setOpeningHours([
      { day: MONDAY, opens: "10:00", closes: "10:00" },
    ]),
  );
  assert.deepEqual(restaurant.openingHours.intervals, [
    { day: MONDAY, opens: "10:00", closes: "22:00" },
  ]);
});

test("AC-941 a night interval running into the next morning is an overlap of that morning", () => {
  assert.throws(
    () =>
      OpeningHours.of([
        { day: FRIDAY, opens: "18:00", closes: "02:00" },
        { day: SATURDAY, opens: "01:00", closes: "12:00" },
      ]),
    (error) =>
      error instanceof InvalidOpeningHoursError &&
      error.reason === InvalidOpeningHoursReason.Overlap &&
      error.interval.day === SATURDAY &&
      error.interval.opens === "01:00" &&
      error.interval.closes === "12:00",
  );
});

test("AC-942 Sunday night touches Monday morning without an overlap", () => {
  const hours = OpeningHours.of([
    { day: SUNDAY, opens: "20:00", closes: "03:00" },
    { day: MONDAY, opens: "03:00", closes: "11:00" },
  ]);
  assert.equal(hours.intervals.length, 2);
  assert.throws(() =>
    OpeningHours.of([
      { day: SUNDAY, opens: "20:00", closes: "03:30" },
      { day: MONDAY, opens: "03:00", closes: "11:00" },
    ]),
  );
});

test("invalid days and times name the interval", () => {
  for (const interval of [
    { day: 0, opens: "10:00", closes: "12:00" },
    { day: 8, opens: "10:00", closes: "12:00" },
    { day: MONDAY, opens: "24:00", closes: "12:00" },
    { day: MONDAY, opens: "9:00", closes: "12:00" },
  ]) {
    assert.throws(
      () => OpeningHours.of([interval]),
      InvalidOpeningHoursError,
      JSON.stringify(interval),
    );
  }
});

test("AC-943 the spring night of Europe/Berlin makes 22:00-04:00 last 5 hours", () => {
  // 2026-03-29: 02:00 CET jumps to 03:00 CEST.
  const hours = OpeningHours.of([
    { day: SATURDAY, opens: "22:00", closes: "04:00" },
  ]);
  const [period] = hours.periodsBetween(
    BERLIN,
    utc("2026-03-28T12:00:00Z"),
    utc("2026-03-29T12:00:00Z"),
  );
  assert.deepEqual(period, {
    opensAt: utc("2026-03-28T21:00:00Z"),
    closesAt: utc("2026-03-29T02:00:00Z"),
  });
  assert.equal(period.closesAt.getTime() - period.opensAt.getTime(), 5 * HOUR);
});

test("AC-944 the autumn night of Europe/Berlin closes 01:00-02:30 at the first 02:30", () => {
  // 2026-10-25: 03:00 CEST falls back to 02:00 CET; 02:30 happens twice.
  const hours = OpeningHours.of([
    { day: SUNDAY, opens: "01:00", closes: "02:30" },
  ]);
  const [period] = hours.periodsBetween(
    BERLIN,
    utc("2026-10-24T12:00:00Z"),
    utc("2026-10-25T12:00:00Z"),
  );
  assert.deepEqual(period, {
    opensAt: utc("2026-10-24T23:00:00Z"),
    closesAt: utc("2026-10-25T00:30:00Z"),
  });
  assert.equal(
    period.closesAt.getTime() - period.opensAt.getTime(),
    90 * MINUTE,
  );
});

test("FR-698 a skipped wall time moves after the gap, a repeated one takes the first occurrence", () => {
  const spring = { year: 2026, month: 3, day: 29, weekday: SUNDAY };
  assert.deepEqual(
    BERLIN.instantOf(spring, 2 * 60 + 30),
    utc("2026-03-29T01:30:00Z"),
    "02:30 does not exist: it is 03:30 CEST",
  );
  const autumn = { year: 2026, month: 10, day: 25, weekday: SUNDAY };
  assert.deepEqual(
    BERLIN.instantOf(autumn, 2 * 60 + 30),
    utc("2026-10-25T00:30:00Z"),
    "02:30 CEST, not 02:30 CET",
  );
  assert.deepEqual(BERLIN.dateOf(utc("2026-10-25T23:30:00Z")), {
    year: 2026,
    month: 10,
    day: 26,
    weekday: MONDAY,
  });
});

test("FR-699 the next opening after now is the start of the next period", () => {
  const hours = OpeningHours.of([
    { day: MONDAY, opens: "10:00", closes: "22:00" },
  ]);
  // Wednesday 2026-10-28 23:00 CET: next Monday 2026-11-02 10:00 CET.
  assert.deepEqual(
    hours.nextOpeningAfter(BERLIN, utc("2026-10-28T22:00:00Z")),
    utc("2026-11-02T09:00:00Z"),
  );
  assert.equal(
    OpeningHours.NONE.nextOpeningAfter(BERLIN, utc("2026-10-28T22:00:00Z")),
    undefined,
  );
});

test("AC-945 AC-946 AC-947 a restaurant accepts orders by the flag and inside the schedule", () => {
  const restaurant = register();
  const noon = utc("2026-10-28T11:00:00Z");
  restaurant.open(noon);
  assert.equal(restaurant.acceptsOrdersAt(noon), false, "AC-947 no schedule");
  restaurant.setOpeningHours(
    [1, 2, 3, 4, 5, 6, 7].map((day) => ({
      day,
      opens: "10:00",
      closes: "22:00",
    })),
  );
  assert.equal(restaurant.acceptsOrdersAt(noon), true);
  assert.equal(
    restaurant.acceptsOrdersAt(utc("2026-10-28T22:00:00Z")),
    false,
    "AC-945 23:00 local",
  );
  restaurant.close(noon);
  assert.equal(restaurant.acceptsOrdersAt(noon), false, "AC-946 closed");
});

test("AC-894 AC-895 settings of scheduled orders are set together and cleared together", () => {
  const restaurant = register();
  const settings = {
    slotCapacity: 4,
    openingLeadMs: 45 * MINUTE,
    closingGraceMs: 10 * MINUTE,
  };
  assert.equal(restaurant.setScheduledOrderSettings(settings), true);
  assert.equal(restaurant.setScheduledOrderSettings(settings), false);
  assert.deepEqual(restaurant.scheduledOrders?.toValue(), settings);
  for (const invalid of [
    { ...settings, slotCapacity: 0 },
    { ...settings, slotCapacity: 1.5 },
    { ...settings, openingLeadMs: -1 },
    { ...settings, closingGraceMs: -1 },
  ]) {
    assert.throws(
      () => restaurant.setScheduledOrderSettings(invalid),
      InvalidScheduledOrderSettingsError,
    );
  }
  assert.equal(restaurant.scheduledOrders?.slotCapacity, 4);
  assert.equal(restaurant.setScheduledOrderSettings(undefined), true);
  assert.equal(restaurant.scheduledOrders, undefined);
  assert.equal(restaurant.toSnapshot().scheduledOrders, undefined);
});
