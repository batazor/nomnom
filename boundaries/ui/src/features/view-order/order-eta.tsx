import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import {
  ariaPercent,
  countdownText,
  type EtaRemaining,
  etaPercent,
  etaRemaining,
  type HeaderEtaKind,
  headerEta,
  minutesLeft,
  type StepEta,
} from "./fulfillment-eta";
import { useDeadlineShift, useNow } from "./use-eta";
import type { ViewedOrder } from "./use-order-updates";

type Translate = ReturnType<typeof useI18n>["t"];

const headerMessage: Record<HeaderEtaKind, MessageKey> = {
  delivered: "order.eta.header.delivered",
  pickup: "order.eta.header.pickup",
  ready: "order.eta.header.ready",
};

// The time left next to a deadline: ~N min, a m:ss countdown, or the delay.
function remainingText(t: Translate, remaining: EtaRemaining): string {
  switch (remaining.kind) {
    case "minutes":
      return t("order.eta.minutes", { minutes: remaining.minutes });
    case "countdown":
      return countdownText(remaining);
    case "overdue":
      return t("order.eta.overdue", { minutes: remaining.minutes });
  }
}

// The best known deadline under the status; none for a final order or
// before anybody named one (FR-612).
export function HeaderEta({ order }: { order: ViewedOrder }) {
  const { t, formatters } = useI18n();
  const eta = headerEta(order);
  const now = useNow(eta !== null);
  if (eta === null) return null;

  const time = formatters.time.format(eta.deadline);
  const remaining = etaRemaining(eta.deadline, now);
  if (remaining.kind === "overdue") {
    return (
      <p className="order-header-eta" data-state="overdue">
        {t("order.eta.header.overdue", { time })}
      </p>
    );
  }
  return (
    <p className="order-header-eta" data-state="running">
      {t(headerMessage[eta.kind], { time })}
      {eta.kind === "delivered" ? (
        <span className="order-header-eta-left">
          {" · "}
          {remainingText(t, remaining)}
        </span>
      ) : null}
    </p>
  );
}

// The inline bar of the active step: elapsed share of the stage, the time
// left and the deadline. It ticks locally and makes no requests (ADR 36).
export function StepEtaBar({ eta }: { eta: StepEta }) {
  const { t, formatters } = useI18n();
  const now = useNow(true);
  const shift = useDeadlineShift(eta.stage, eta.deadline);

  const time = formatters.time.format(eta.deadline);
  const remaining = etaRemaining(eta.deadline, now);
  const overdue = remaining.kind === "overdue";
  const percent = overdue ? 100 : etaPercent(eta, now);
  // Minute granularity: assistive technology is not told every second.
  const valueText = overdue
    ? `${remainingText(t, remaining)}, ${t("order.eta.expectedBy", { time })}`
    : `${t("order.eta.remaining", {
        minutes: minutesLeft(eta.deadline, now),
      })}, ${t("order.eta.until", { time })}`;

  return (
    <div className="order-step-eta" data-state={overdue ? "overdue" : "running"}>
      <div
        aria-label={t(eta.label)}
        aria-valuemax={100}
        aria-valuemin={0}
        aria-valuenow={ariaPercent(percent)}
        aria-valuetext={valueText}
        className="order-step-eta-track"
        role="progressbar"
      >
        <span
          className="order-step-eta-fill"
          style={{ inlineSize: `${percent}%` }}
        />
      </div>
      <p aria-hidden="true" className="order-step-eta-note">
        <span className="order-step-eta-left">
          {remainingText(t, remaining)}
        </span>
        {" · "}
        {overdue
          ? t("order.eta.expectedBy", { time })
          : t("order.eta.until", { time })}
        {shift === null ? null : (
          <span className="order-step-eta-shift">
            {t("order.eta.shift", { minutes: shift })}
          </span>
        )}
      </p>
      <p aria-live="polite" className="sr-only">
        {shift === null
          ? ""
          : t("order.eta.shiftAnnounce", { minutes: shift, time })}
      </p>
    </div>
  );
}
