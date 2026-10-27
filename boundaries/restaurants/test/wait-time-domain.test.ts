import assert from "node:assert/strict";
import test from "node:test";

import {
  InvalidWaitTimePolicyError,
  NO_WAIT_TIME_STATISTICS,
  WaitTimePolicy,
  type WaitTimeStatistics,
} from "../slices/restaurant/domains/restaurant/wait-time.ts";

const MINUTE = 60_000;
const now = new Date("2026-10-27T12:00:00.000Z");
const minutesAgo = (minutes: number) =>
  new Date(now.getTime() - minutes * MINUTE);

const policyOf = (sampleSize = 4) =>
  new WaitTimePolicy({
    sampleSize,
    recentWindowMs: 30 * MINUTE,
    elevatedFactor: 1.5,
    normalFactor: 1.2,
    preparationTimeMs: 20 * MINUTE,
  });

/** Sample of `durations` minutes, ready long before the recent window. */
const sampled = (
  durations: readonly number[],
  extra: Partial<WaitTimeStatistics> = {},
): WaitTimeStatistics => ({
  ...NO_WAIT_TIME_STATISTICS,
  completed: durations.map((minutes, index) => ({
    orderId: `old-${index}`,
    readyAt: minutesAgo(120),
    durationMs: minutes * MINUTE,
  })),
  ...extra,
});

test("AC-843 without a complete sample the baseline and current wait are the preparation time", () => {
  const policy = policyOf();
  assert.deepEqual(policy.estimate(NO_WAIT_TIME_STATISTICS, now), {
    baselineMs: 20 * MINUTE,
    currentMs: 20 * MINUTE,
  });
  assert.equal(policy.level(NO_WAIT_TIME_STATISTICS, now), "NORMAL");
});

test("FR-645 the baseline is the median of the sample, the recent median raises the current wait", () => {
  const policy = policyOf();
  const statistics = sampled([10, 30, 20, 40], {
    completed: [
      ...sampled([10, 30, 20]).completed,
      { orderId: "recent", readyAt: minutesAgo(5), durationMs: 40 * MINUTE },
    ],
  });
  assert.deepEqual(policy.estimate(statistics, now), {
    baselineMs: 25 * MINUTE,
    currentMs: 40 * MINUTE,
  });
});

test("AC-841 the age of the oldest open order makes the kitchen elevated", () => {
  const policy = policyOf();
  const statistics = sampled([20, 20, 20, 20], {
    open: [
      { orderId: "late", acceptedAt: minutesAgo(35) },
      { orderId: "fresh", acceptedAt: minutesAgo(2) },
    ],
  });
  assert.equal(policy.estimate(statistics, now).currentMs, 35 * MINUTE);
  assert.equal(policy.level(statistics, now), "ELEVATED");
});

test("AC-842 the level keeps between the thresholds", () => {
  const policy = policyOf();
  const waiting = (minutes: number, level: "NORMAL" | "ELEVATED") =>
    sampled([20, 20, 20, 20], {
      level,
      open: [{ orderId: "o", acceptedAt: minutesAgo(minutes) }],
    });
  assert.equal(policy.level(waiting(26, "ELEVATED"), now), "ELEVATED");
  assert.equal(policy.level(waiting(26, "NORMAL"), now), "NORMAL");
  assert.equal(policy.level(waiting(24, "ELEVATED"), now), "NORMAL");
});

test("AC-837 a ready order moves its duration to the sample, the oldest leaves", () => {
  const policy = policyOf(2);
  let statistics = policy.accepted(
    NO_WAIT_TIME_STATISTICS,
    "a",
    minutesAgo(30),
  );
  statistics = policy.ready(statistics, "a", minutesAgo(12));
  statistics = policy.accepted(statistics, "b", minutesAgo(10));
  statistics = policy.ready(statistics, "b", minutesAgo(5));
  statistics = policy.accepted(statistics, "c", minutesAgo(4));
  statistics = policy.ready(statistics, "c", minutesAgo(1));
  assert.deepEqual(
    statistics.completed.map((entry) => [entry.orderId, entry.durationMs]),
    [
      ["b", 5 * MINUTE],
      ["c", 3 * MINUTE],
    ],
  );
  assert.deepEqual(statistics.open, []);
});

test("AC-838 repeated and unknown facts change nothing", () => {
  const policy = policyOf();
  const accepted = policy.accepted(NO_WAIT_TIME_STATISTICS, "a", minutesAgo(9));
  assert.equal(policy.accepted(accepted, "a", minutesAgo(9)), accepted);
  const ready = policy.ready(accepted, "a", minutesAgo(1));
  assert.equal(policy.ready(ready, "a", minutesAgo(1)), ready);
  assert.equal(policy.accepted(ready, "a", minutesAgo(9)), ready);
  assert.equal(policy.dropped(ready, "unknown"), ready);
});

test("AC-839 a dropped order leaves the open orders without a duration", () => {
  const policy = policyOf();
  const accepted = policy.accepted(NO_WAIT_TIME_STATISTICS, "a", minutesAgo(9));
  const dropped = policy.dropped(accepted, "a");
  assert.deepEqual(dropped.open, []);
  assert.deepEqual(dropped.completed, []);
});

test("FR-657 the policy rejects inverted thresholds and empty parameters", () => {
  const valid = {
    sampleSize: 50,
    recentWindowMs: 30 * MINUTE,
    elevatedFactor: 1.5,
    normalFactor: 1.2,
    preparationTimeMs: 20 * MINUTE,
  };
  for (const invalid of [
    { normalFactor: 1.5 },
    { sampleSize: 0 },
    { recentWindowMs: 0 },
    { preparationTimeMs: Number.NaN },
  ]) {
    assert.throws(
      () => new WaitTimePolicy({ ...valid, ...invalid }),
      InvalidWaitTimePolicyError,
    );
  }
});
