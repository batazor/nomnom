import type { ZonedFormatters } from "../../shared/i18n/zoned-time";

// Delivery window of a scheduled order as BFF lists it: moments in RFC 3339.
export type DeliveryWindow = Readonly<{
  start: string;
  end: string;
  available: boolean;
}>;

export type WindowChoice = Readonly<{
  window: DeliveryWindow;
  // Wall-clock label in the Market time zone, for example "19:00–19:15";
  // with the UTC offset when the day has the same start twice.
  label: string;
}>;

export type WindowDay = Readonly<{
  key: string;
  label: string;
  windows: readonly WindowChoice[];
}>;

// Windows by day of the Market (FR-686), in the order BFF lists them (by
// start). The night the clocks go back repeats an hour: its windows have the
// same local labels, so each of them names its offset ("02:00–02:15 GMT+2",
// "02:00–02:15 GMT+1"). The choice is sent as the start moment, never as the
// label, so the server is never confused.
export function groupWindows(
  windows: readonly DeliveryWindow[],
  zoned: ZonedFormatters,
): WindowDay[] {
  const days = new Map<string, DeliveryWindow[]>();
  for (const window of windows) {
    const key = zoned.dateKey(window.start);
    const day = days.get(key);
    if (day) day.push(window);
    else days.set(key, [window]);
  }
  return [...days].map(([key, dayWindows]) => {
    const starts = dayWindows.map((window) => zoned.time(window.start));
    return {
      key,
      label: zoned.day(dayWindows[0].start),
      windows: dayWindows.map((window, index) => {
        const label = zoned.window(window.start, window.end);
        const repeated =
          starts.indexOf(starts[index]) !== starts.lastIndexOf(starts[index]);
        return {
          window,
          label: repeated ? `${label} ${zoned.offset(window.start)}` : label,
        };
      }),
    };
  });
}
