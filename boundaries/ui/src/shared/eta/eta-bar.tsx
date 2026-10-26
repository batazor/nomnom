import { useI18n } from "../i18n/i18n";
import type { MessageKey } from "../i18n/messages";
import {
  ariaPercent,
  countdownText,
  type EtaRemaining,
  type EtaSpan,
  etaPercent,
  etaRemaining,
  minutesLeft,
} from "./eta";
import { useDeadlineShift, useNow } from "./use-eta";

import "./eta-bar.css";

type Translate = ReturnType<typeof useI18n>["t"];

// How a screen words the time left before the deadline: `{minutes}` for
// ~N min, `{countdown}` for m:ss. A missed deadline is always eta.overdue.
export type RemainingCopy = Readonly<{
  minutes: MessageKey;
  countdown: MessageKey;
}>;

// The time left next to a deadline: ~N min, a m:ss countdown, or the delay.
export function remainingText(
  t: Translate,
  remaining: EtaRemaining,
  copy: RemainingCopy,
): string {
  switch (remaining.kind) {
    case "minutes":
      return t(copy.minutes, { minutes: remaining.minutes });
    case "countdown":
      return t(copy.countdown, { countdown: countdownText(remaining) });
    case "overdue":
      return t("eta.overdue", { minutes: remaining.minutes });
  }
}

// An inline bar from the start of a stage to its deadline: elapsed share,
// the time left and the deadline. It ticks locally each second and makes no
// requests (ADR 36); a deadline of the same stage moved later shows +N min
// for a few seconds.
export function EtaBar({
  className,
  copy,
  label,
  span,
  stage,
}: {
  // Placement of the bar on its screen; the bar itself is styled here.
  className?: string;
  copy: RemainingCopy;
  // Accessible name of the progressbar.
  label: string;
  span: EtaSpan;
  // A new stage starts without a shift.
  stage: string;
}) {
  const { t, formatters } = useI18n();
  const now = useNow(true);
  const shift = useDeadlineShift(stage, span.deadline);

  const time = formatters.time.format(span.deadline);
  const remaining = etaRemaining(span.deadline, now);
  const overdue = remaining.kind === "overdue";
  const percent = overdue ? 100 : etaPercent(span, now);
  // Minute granularity: assistive technology is not told every second.
  const valueText = overdue
    ? `${remainingText(t, remaining, copy)}, ${t("eta.expectedBy", { time })}`
    : `${t("eta.remaining", {
        minutes: minutesLeft(span.deadline, now),
      })}, ${t("eta.until", { time })}`;

  return (
    <div
      className={className === undefined ? "eta-bar" : `eta-bar ${className}`}
      data-state={overdue ? "overdue" : "running"}
    >
      <div
        aria-label={label}
        aria-valuemax={100}
        aria-valuemin={0}
        aria-valuenow={ariaPercent(percent)}
        aria-valuetext={valueText}
        className="eta-bar-track"
        role="progressbar"
      >
        <span className="eta-bar-fill" style={{ inlineSize: `${percent}%` }} />
      </div>
      <p aria-hidden="true" className="eta-bar-note">
        <span className="eta-bar-left">
          {remainingText(t, remaining, copy)}
        </span>
        {" · "}
        {overdue ? t("eta.expectedBy", { time }) : t("eta.until", { time })}
        {shift === null ? null : (
          <span className="eta-bar-shift">
            {t("eta.shift", { minutes: shift })}
          </span>
        )}
      </p>
      <p aria-live="polite" className="sr-only">
        {shift === null ? "" : t("eta.shiftAnnounce", { minutes: shift, time })}
      </p>
    </div>
  );
}
