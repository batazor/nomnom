import { useEffect, useState } from "react";

import { deadlineShiftMinutes } from "./eta";

// The clock re-renders the deadlines between events; it makes no requests
// (ADR 36). It ticks only while something counts towards a deadline.
const tickMs = 1_000;

export function useNow(running: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!running) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), tickMs);
    return () => clearInterval(timer);
  }, [running]);
  return now;
}

// How long the "+N min" chip stays after a deadline moved later.
export const shiftShownMs = 5_000;

type Seen = { stage: string | null; deadline: number | null };

// Minutes the deadline of the same stage moved later by the last update, for
// shiftShownMs; null otherwise. A new stage starts without a shift.
export function useDeadlineShift(
  stage: string | null,
  deadline: number | null,
): number | null {
  const [seen, setSeen] = useState<Seen>({ stage, deadline });
  const [shift, setShift] = useState<{ minutes: number; deadline: number }>();

  if (seen.stage !== stage || seen.deadline !== deadline) {
    const minutes =
      seen.stage === stage && seen.deadline !== null && deadline !== null
        ? deadlineShiftMinutes(seen.deadline, deadline)
        : null;
    setSeen({ stage, deadline });
    if (minutes !== null && deadline !== null) {
      setShift({ minutes, deadline });
    } else if (seen.stage !== stage) {
      setShift(undefined);
    }
  }

  useEffect(() => {
    if (shift === undefined) return;
    const timer = setTimeout(() => setShift(undefined), shiftShownMs);
    return () => clearTimeout(timer);
  }, [shift]);

  return shift === undefined ? null : shift.minutes;
}
