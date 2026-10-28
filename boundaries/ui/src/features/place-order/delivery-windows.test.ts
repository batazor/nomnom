import { describe, expect, it } from "vitest";

import { createZonedFormatters } from "../../shared/i18n/zoned-time";
import { type DeliveryWindow, groupWindows } from "./delivery-windows";

const berlin = createZonedFormatters("ru", "Europe/Berlin");

// Windows of 15 minutes on the UTC grid from start, count of them.
function windows(start: string, count: number): DeliveryWindow[] {
  const first = Date.parse(start);
  return Array.from({ length: count }, (_, index) => ({
    start: new Date(first + index * 15 * 60_000).toISOString(),
    end: new Date(first + (index + 1) * 15 * 60_000).toISOString(),
    available: index !== 1,
  }));
}

describe("delivery windows", () => {
  it("groups windows by day of the Market, not of the browser", () => {
    // 23:00 UTC is already the next day in Berlin (CET, UTC+1).
    const days = groupWindows(windows("2026-10-28T22:30:00Z", 4), berlin);

    expect(days.map((day) => day.key)).toEqual(["2026-10-28", "2026-10-29"]);
    expect(days[0].windows.map((choice) => choice.label)).toEqual([
      "23:30–23:45",
      "23:45–00:00",
    ]);
    expect(days[1].windows.map((choice) => choice.label)).toEqual([
      "00:00–00:15",
      "00:15–00:30",
    ]);
    // A full window stays in the list, marked unavailable.
    expect(days[0].windows[1].window.available).toBe(false);
  });

  // AC-930: the night the clocks go back repeats 02:00–03:00; its windows
  // carry the offset, other windows of the day do not.
  it("tells the repeated hour apart by its offset", () => {
    // 23:45 UTC is 01:45 CEST; at 01:00 UTC 03:00 CEST becomes 02:00 CET.
    const days = groupWindows(windows("2026-10-24T23:45:00Z", 10), berlin);

    expect(days).toHaveLength(1);
    expect(days[0].windows.map((choice) => choice.label)).toEqual([
      "01:45–02:00",
      "02:00–02:15 GMT+2",
      "02:15–02:30 GMT+2",
      "02:30–02:45 GMT+2",
      "02:45–02:00 GMT+2",
      "02:00–02:15 GMT+1",
      "02:15–02:30 GMT+1",
      "02:30–02:45 GMT+1",
      "02:45–03:00 GMT+1",
      "03:00–03:15",
    ]);
  });
});
