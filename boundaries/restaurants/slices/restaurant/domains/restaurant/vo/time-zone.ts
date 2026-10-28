const MINUTE_MS = 60_000;
const DAY_MS = 86_400_000;

/** Calendar date without a time zone; `weekday` is ISO: 1 Monday … 7 Sunday. */
export type LocalDate = Readonly<{
  year: number;
  month: number;
  day: number;
  weekday: number;
}>;

const localDate = (utcMidnightMs: number): LocalDate => {
  const date = new Date(utcMidnightMs);
  return {
    year: date.getUTCFullYear(),
    month: date.getUTCMonth() + 1,
    day: date.getUTCDate(),
    weekday: ((date.getUTCDay() + 6) % 7) + 1,
  };
};

/** Not an IANA time zone known to the runtime (FR-201). */
export class InvalidTimeZoneError extends Error {
  readonly code = "invalid_time_zone";
  readonly value: string;

  constructor(value: string) {
    super(`unknown IANA time zone: ${JSON.stringify(value)}`);
    this.name = "InvalidTimeZoneError";
    this.value = value;
  }
}

/**
 * IANA time zone of a restaurant (FR-201). Converts between instants and the
 * wall clock of the restaurant; opening hours are kept on the wall clock and
 * every check works with instants (FR-698).
 */
export class TimeZone {
  readonly value: string;
  readonly #format: Intl.DateTimeFormat;

  private constructor(value: string, format: Intl.DateTimeFormat) {
    this.value = value;
    this.#format = format;
  }

  static of(value: string): TimeZone {
    if (value.length === 0 || value !== value.trim()) {
      throw new InvalidTimeZoneError(value);
    }
    try {
      // An unknown zone makes Intl throw RangeError.
      const format = new Intl.DateTimeFormat("en-US", {
        timeZone: value,
        hourCycle: "h23",
        year: "numeric",
        month: "numeric",
        day: "numeric",
        hour: "numeric",
        minute: "numeric",
        second: "numeric",
      });
      return new TimeZone(value, format);
    } catch {
      throw new InvalidTimeZoneError(value);
    }
  }

  /** Offset of the wall clock from UTC at `instantMs`, in milliseconds. */
  #offsetMs(instantMs: number): number {
    const parts: Record<string, number> = {};
    for (const part of this.#format.formatToParts(new Date(instantMs))) {
      if (part.type !== "literal") parts[part.type] = Number(part.value);
    }
    const wallMs = Date.UTC(
      parts.year,
      parts.month - 1,
      parts.day,
      parts.hour,
      parts.minute,
      parts.second,
    );
    return Math.round((wallMs - instantMs) / MINUTE_MS) * MINUTE_MS;
  }

  /** Local date of `instant` on the wall clock of this zone. */
  dateOf(instant: Date): LocalDate {
    const wallMs = instant.getTime() + this.#offsetMs(instant.getTime());
    return localDate(Math.floor(wallMs / DAY_MS) * DAY_MS);
  }

  /** The local date `days` after `date`. */
  static addDays(date: LocalDate, days: number): LocalDate {
    return localDate(
      Date.UTC(date.year, date.month - 1, date.day) + days * DAY_MS,
    );
  }

  /**
   * Instant of `minutes` after local midnight of `date`. A wall time skipped
   * by a clock change moves to the first instant after the gap; a repeated
   * wall time resolves to its first occurrence (FR-698).
   */
  instantOf(date: LocalDate, minutes: number): Date {
    const wallMs =
      Date.UTC(date.year, date.month - 1, date.day) + minutes * MINUTE_MS;
    const before = this.#offsetMs(wallMs - DAY_MS);
    const after = this.#offsetMs(wallMs + DAY_MS);
    const occurrences = [wallMs - before, wallMs - after].filter(
      (instantMs) => instantMs + this.#offsetMs(instantMs) === wallMs,
    );
    if (occurrences.length === 0) {
      // Skipped wall time: read with the offset before the gap, it lands after it.
      return new Date(wallMs - before);
    }
    return new Date(Math.min(...occurrences));
  }
}
