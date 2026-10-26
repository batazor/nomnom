// Time left to a deadline someone named, counted between events without
// requests (ADR 36). Pure functions: the clock comes in as `now`.

const minuteMs = 60_000;
// Up to this much time left a deadline counts down m:ss every second.
export const countdownMs = 5 * minuteMs;

// A stage that runs from a known start to a named deadline, epoch ms.
export type EtaSpan = Readonly<{ startedAt: number; deadline: number }>;

export type EtaRemaining =
  // More than five minutes left: whole minutes, rounded up.
  | { kind: "minutes"; minutes: number }
  // Five minutes or less: a m:ss countdown.
  | { kind: "countdown"; minutes: number; seconds: number }
  // The deadline passed this many minutes ago, rounded up.
  | { kind: "overdue"; minutes: number };

export function etaRemaining(deadline: number, now: number): EtaRemaining {
  const left = deadline - now;
  if (left <= 0) {
    return {
      kind: "overdue",
      minutes: Math.max(1, Math.ceil(-left / minuteMs)),
    };
  }
  if (left > countdownMs) {
    return { kind: "minutes", minutes: Math.ceil(left / minuteMs) };
  }
  const totalSeconds = Math.ceil(left / 1_000);
  return {
    kind: "countdown",
    minutes: Math.floor(totalSeconds / 60),
    seconds: totalSeconds % 60,
  };
}

// The m:ss text of a countdown.
export function countdownText(
  remaining: Extract<EtaRemaining, { kind: "countdown" }>,
): string {
  return `${remaining.minutes}:${String(remaining.seconds).padStart(2, "0")}`;
}

// Whole minutes left, rounded up, for text that must not change every second.
export function minutesLeft(deadline: number, now: number): number {
  return Math.max(1, Math.ceil((deadline - now) / minuteMs));
}

// Elapsed share of the span, 0..100, not rounded.
export function etaPercent(span: EtaSpan, now: number): number {
  const total = span.deadline - span.startedAt;
  if (total <= 0) return 100;
  return Math.min(100, Math.max(0, ((now - span.startedAt) / total) * 100));
}

// aria-valuenow moves in steps of 5%, not with every tick.
export function ariaPercent(percent: number): number {
  return Math.min(100, Math.max(0, Math.round(percent / 5) * 5));
}

// Whole minutes a deadline of the same stage moved later; null if it did not.
export function deadlineShiftMinutes(
  previous: number,
  next: number,
): number | null {
  const minutes = Math.round((next - previous) / minuteMs);
  return minutes > 0 ? minutes : null;
}
