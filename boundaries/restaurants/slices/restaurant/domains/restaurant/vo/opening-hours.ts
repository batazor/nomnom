import { type LocalDate, TimeZone } from "./time-zone.ts";

export const InvalidOpeningHoursReason = {
  InvalidDay: "invalid_day",
  InvalidTime: "invalid_time",
  EmptyInterval: "empty_interval",
  Overlap: "overlap",
} as const;

export type InvalidOpeningHoursReason =
  (typeof InvalidOpeningHoursReason)[keyof typeof InvalidOpeningHoursReason];

/** Rejected weekly schedule; names the offending interval (FR-696, FR-697). */
export class InvalidOpeningHoursError extends Error {
  readonly code = "invalid_opening_hours";
  readonly reason: InvalidOpeningHoursReason;
  readonly interval: Readonly<{ day: number; opens: string; closes: string }>;

  constructor(
    reason: InvalidOpeningHoursReason,
    interval: Readonly<{ day: number; opens: string; closes: string }>,
  ) {
    super(
      `invalid opening hours: ${reason} at day ${interval.day} ${interval.opens}-${interval.closes}`,
    );
    this.name = "InvalidOpeningHoursError";
    this.reason = reason;
    this.interval = interval;
  }
}

const DAY_MINUTES = 1440;
const WEEK_MINUTES = 7 * DAY_MINUTES;
const WEEK_DAYS = 7;
const TIME = /^([01]\d|2[0-3]):([0-5]\d)$/;

/**
 * One interval of the weekly schedule on the wall clock of the restaurant:
 * `day` is ISO (1 Monday … 7 Sunday), `opens` and `closes` are `HH:MM`.
 * `closes <= opens` closes on the next day (FR-696).
 */
export type OpeningInterval = Readonly<{
  day: number;
  opens: string;
  closes: string;
}>;

/** An interval of the schedule on a concrete date, as instants. */
export type OpeningPeriod = Readonly<{ opensAt: Date; closesAt: Date }>;

const minutesOf = (time: string, interval: OpeningInterval): number => {
  const match = TIME.exec(time);
  if (match === null) {
    throw new InvalidOpeningHoursError(
      InvalidOpeningHoursReason.InvalidTime,
      interval,
    );
  }
  return Number(match[1]) * 60 + Number(match[2]);
};

type WeekSpan = Readonly<{
  interval: OpeningInterval;
  start: number;
  end: number;
}>;

const weekSpan = (interval: OpeningInterval): WeekSpan => {
  if (
    !Number.isInteger(interval.day) ||
    interval.day < 1 ||
    interval.day > WEEK_DAYS
  ) {
    throw new InvalidOpeningHoursError(
      InvalidOpeningHoursReason.InvalidDay,
      interval,
    );
  }
  const opens = minutesOf(interval.opens, interval);
  const closes = minutesOf(interval.closes, interval);
  if (opens === closes) {
    throw new InvalidOpeningHoursError(
      InvalidOpeningHoursReason.EmptyInterval,
      interval,
    );
  }
  const start = (interval.day - 1) * DAY_MINUTES + opens;
  const duration =
    closes > opens ? closes - opens : DAY_MINUTES - opens + closes;
  return { interval, start, end: start + duration };
};

/** Spans overlap on the week circle: Sunday night continues into Monday. */
const overlaps = (a: WeekSpan, b: WeekSpan): boolean =>
  [-WEEK_MINUTES, 0, WEEK_MINUTES].some(
    (shift) => a.start < b.end + shift && b.start + shift < a.end,
  );

/**
 * Weekly opening hours of a restaurant (FR-696, FR-697). Kept on the wall
 * clock; {@link periodsBetween} turns them into instants in the restaurant
 * time zone (FR-698). No intervals means the restaurant never accepts orders
 * by schedule.
 */
export class OpeningHours {
  readonly intervals: readonly OpeningInterval[];

  private constructor(intervals: readonly OpeningInterval[]) {
    this.intervals = intervals;
  }

  static readonly NONE = new OpeningHours([]);

  static of(intervals: readonly OpeningInterval[]): OpeningHours {
    const spans = intervals.map((interval) => weekSpan(interval));
    spans.sort((a, b) => a.start - b.start);
    for (let i = 0; i < spans.length; i += 1) {
      for (let j = i + 1; j < spans.length; j += 1) {
        if (overlaps(spans[i], spans[j])) {
          throw new InvalidOpeningHoursError(
            InvalidOpeningHoursReason.Overlap,
            spans[j].interval,
          );
        }
      }
    }
    return new OpeningHours(
      spans.map(({ interval }) => ({
        day: interval.day,
        opens: interval.opens,
        closes: interval.closes,
      })),
    );
  }

  get isEmpty(): boolean {
    return this.intervals.length === 0;
  }

  equals(other: OpeningHours): boolean {
    return (
      this.intervals.length === other.intervals.length &&
      this.intervals.every(
        (interval, index) =>
          interval.day === other.intervals[index].day &&
          interval.opens === other.intervals[index].opens &&
          interval.closes === other.intervals[index].closes,
      )
    );
  }

  /** Periods of the schedule that overlap `[from, to)`, ordered by opening. */
  periodsBetween(zone: TimeZone, from: Date, to: Date): OpeningPeriod[] {
    const periods: OpeningPeriod[] = [];
    // A period opened the day before may still run at `from`.
    const last = zone.dateOf(to);
    for (
      let date: LocalDate = TimeZone.addDays(zone.dateOf(from), -1);
      Date.UTC(date.year, date.month - 1, date.day) <=
      Date.UTC(last.year, last.month - 1, last.day);
      date = TimeZone.addDays(date, 1)
    ) {
      for (const interval of this.intervals) {
        if (interval.day !== date.weekday) continue;
        const opens = minutesOf(interval.opens, interval);
        const closes = minutesOf(interval.closes, interval);
        const period = {
          opensAt: zone.instantOf(date, opens),
          closesAt: zone.instantOf(
            closes > opens ? date : TimeZone.addDays(date, 1),
            closes,
          ),
        };
        if (period.closesAt > from && period.opensAt < to) periods.push(period);
      }
    }
    return periods.sort((a, b) => a.opensAt.getTime() - b.opensAt.getTime());
  }

  /** The period running at `at`, if any. */
  periodAt(zone: TimeZone, at: Date): OpeningPeriod | undefined {
    return this.periodsBetween(zone, at, new Date(at.getTime() + 1)).find(
      (period) => period.opensAt <= at && at < period.closesAt,
    );
  }

  /** Opening of the first period starting after `at`, within a week. */
  nextOpeningAfter(zone: TimeZone, at: Date): Date | undefined {
    const weekLater = new Date(
      at.getTime() + (WEEK_DAYS + 1) * DAY_MINUTES * 60_000,
    );
    return this.periodsBetween(zone, at, weekLater).find(
      (period) => period.opensAt > at,
    )?.opensAt;
  }
}
