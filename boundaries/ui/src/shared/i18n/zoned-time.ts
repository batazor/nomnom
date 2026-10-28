import { useMemo } from "react";

import { useI18n } from "./i18n";

// Moments of orders and restaurants are shown on the wall clock of their
// place — the Market or the restaurant — not of the browser: a customer and
// the restaurant are in one Market, and its time is what they agree on.
export function createZonedFormatters(locale: string, timeZone: string) {
  const time = new Intl.DateTimeFormat(locale, {
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
    timeZone,
  });
  const day = new Intl.DateTimeFormat(locale, {
    weekday: "short",
    day: "numeric",
    month: "long",
    timeZone,
  });
  // Calendar date of a moment in the zone, as a sortable key.
  const date = new Intl.DateTimeFormat("en-CA", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    timeZone,
  });
  const offset = new Intl.DateTimeFormat("en-US", {
    timeZoneName: "shortOffset",
    timeZone,
  });
  return {
    time: (moment: string | number) => time.format(new Date(moment)),
    day: (moment: string | number) => day.format(new Date(moment)),
    dateKey: (moment: string | number) => date.format(new Date(moment)),
    // UTC offset of the zone at the moment, for example "GMT+2": the only
    // difference of the two equal hours of the night the clocks go back.
    offset: (moment: string | number) => {
      const name = offset
        .formatToParts(new Date(moment))
        .find((part) => part.type === "timeZoneName");
      if (!name) throw new Error(`no UTC offset of ${timeZone}`);
      return name.value;
    },
    window: (start: string, end: string) =>
      `${time.format(new Date(start))}–${time.format(new Date(end))}`,
  };
}

export type ZonedFormatters = ReturnType<typeof createZonedFormatters>;

// Null while the zone is not known yet (the Markets still load).
export function useZonedFormatters(
  timeZone: string | null,
): ZonedFormatters | null {
  const { locale } = useI18n();
  return useMemo(
    () => (timeZone === null ? null : createZonedFormatters(locale, timeZone)),
    [locale, timeZone],
  );
}
