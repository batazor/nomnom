import { describe, expect, it } from "vitest";

import {
  ariaPercent,
  countdownText,
  deadlineShiftMinutes,
  etaPercent,
  etaRemaining,
} from "./eta";

describe("eta formatting", () => {
  const minute = 60_000;

  it("rounds minutes up above five minutes", () => {
    expect(etaRemaining(12 * minute, 0)).toEqual({
      kind: "minutes",
      minutes: 12,
    });
    expect(etaRemaining(5 * minute + 1, 0)).toEqual({
      kind: "minutes",
      minutes: 6,
    });
  });

  it("counts down m:ss from five minutes", () => {
    const five = etaRemaining(5 * minute, 0);
    expect(five).toEqual({ kind: "countdown", minutes: 5, seconds: 0 });
    const short = etaRemaining(4 * minute + 59_400, 0);
    expect(short.kind === "countdown" && countdownText(short)).toBe("5:00");
    const last = etaRemaining(9_000, 0);
    expect(last.kind === "countdown" && countdownText(last)).toBe("0:09");
  });

  it("reports a missed deadline in whole minutes of delay", () => {
    expect(etaRemaining(0, 0)).toEqual({ kind: "overdue", minutes: 1 });
    expect(etaRemaining(0, 2 * minute + 1)).toEqual({
      kind: "overdue",
      minutes: 3,
    });
  });

  it("measures progress and rounds aria-valuenow to 5%", () => {
    const eta = { startedAt: 0, deadline: 10 * minute };
    expect(etaPercent(eta, 0)).toBe(0);
    expect(etaPercent(eta, 3.3 * minute)).toBeCloseTo(33);
    expect(etaPercent(eta, 11 * minute)).toBe(100);
    expect(ariaPercent(33)).toBe(35);
    expect(ariaPercent(32.4)).toBe(30);
    expect(ariaPercent(100)).toBe(100);
  });

  it("names only a later deadline as a shift", () => {
    expect(deadlineShiftMinutes(0, 6 * minute)).toBe(6);
    expect(deadlineShiftMinutes(6 * minute, 0)).toBeNull();
    expect(deadlineShiftMinutes(0, 10_000)).toBeNull();
  });
});
