import assert from "node:assert/strict";
import test from "node:test";
import {
  MenuItemAvailabilityReason,
  MenuItemAvailable,
} from "../slices/menu/domains/menu/menu.ts";
import {
  RestaurantAcceptanceReason,
  RestaurantAcceptingOrders,
} from "../slices/restaurant/domains/restaurant/restaurant.ts";

const at = (hour: number, minute = 0): Date =>
  new Date(Date.UTC(2026, 9, 24, hour, minute));

test("RestaurantAcceptingOrders accepts opening boundary with spare capacity", () => {
  const result = RestaurantAcceptingOrders({
    now: at(10),
    opensAt: at(10),
    closesAt: at(12),
    currentOrders: 4,
    maximumOrders: 5,
    scheduleException: false,
  });

  assert.equal(result.satisfied, true);
  assert.deepEqual(result.reasons, []);
});

test("RestaurantAcceptingOrders collects schedule, hours and capacity reasons", () => {
  const result = RestaurantAcceptingOrders({
    now: at(12),
    opensAt: at(10),
    closesAt: at(12),
    currentOrders: 5,
    maximumOrders: 5,
    scheduleException: true,
  });

  assert.equal(result.satisfied, false);
  assert.deepEqual(result.reasons, [
    RestaurantAcceptanceReason.OutsideOpeningHours,
    RestaurantAcceptanceReason.ScheduleException,
    RestaurantAcceptanceReason.CapacityExhausted,
  ]);
});

test("MenuItemAvailable accepts both time boundaries and exact capacity", () => {
  for (const now of [at(10), at(11)]) {
    const result = MenuItemAvailable({
      now,
      availableFrom: at(10),
      availableUntil: at(11),
      requestedQuantity: 2,
      remainingCapacity: 2,
      temporarilyUnavailable: false,
    });

    assert.equal(result.satisfied, true);
    assert.deepEqual(result.reasons, []);
  }
});

test("MenuItemAvailable collects window, temporary and capacity reasons", () => {
  const result = MenuItemAvailable({
    now: at(12),
    availableFrom: at(10),
    availableUntil: at(11),
    requestedQuantity: 2,
    remainingCapacity: 1,
    temporarilyUnavailable: true,
  });

  assert.equal(result.satisfied, false);
  assert.deepEqual(result.reasons, [
    MenuItemAvailabilityReason.OutsideAvailabilityWindow,
    MenuItemAvailabilityReason.TemporarilyUnavailable,
    MenuItemAvailabilityReason.InsufficientCapacity,
  ]);
});
