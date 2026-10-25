import { useEffect, useId, useState } from "react";

import { useI18n } from "../../shared/i18n/i18n";
import {
  type EtaBar,
  type EtaRemaining,
  etaBars,
  etaProgress,
  runsClock,
} from "./fulfillment-eta";
import type { ViewedOrder } from "./use-order-updates";

// The clock re-renders the bars between events; it makes no requests.
const tickMs = 1_000;

function useNow(running: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!running) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), tickMs);
    return () => clearInterval(timer);
  }, [running]);
  return now;
}

// Cooking and delivery bars of an order on its way (FR-612).
export function FulfillmentProgress({ order }: { order: ViewedOrder }) {
  const { t } = useI18n();
  const bars = etaBars(order);
  const now = useNow(runsClock(bars));

  if (bars.length === 0) return null;

  return (
    <section aria-labelledby="order-eta-title" className="order-card order-eta">
      <h4 className="sr-only" id="order-eta-title">
        {t("order.eta.title")}
      </h4>
      {bars.map((found) => (
        <EtaRow bar={found} key={found.stage} now={now} />
      ))}
    </section>
  );
}

function EtaRow({ bar, now }: { bar: EtaBar; now: number }) {
  const { t, formatters } = useI18n();
  const labelId = useId();
  const time = (value: number | string) =>
    formatters.time.format(
      typeof value === "string" ? Date.parse(value) : value,
    );

  if (bar.kind === "done") {
    return (
      <div className="order-eta-row" data-state="done">
        <span className="order-eta-label">{t(bar.label)}</span>
        <span className="order-eta-note">
          {bar.finishedAt === null
            ? t("order.eta.done")
            : t("order.eta.doneAt", { time: time(bar.finishedAt) })}
        </span>
      </div>
    );
  }

  if (bar.kind === "unnamed") {
    return (
      <div className="order-eta-row" data-state="unnamed">
        <span className="order-eta-label">{t(bar.label)}</span>
        <span className="order-eta-note">{t("order.eta.unnamed")}</span>
      </div>
    );
  }

  const { percent, remaining } = etaProgress(bar, now);
  const left = remainingText(t, remaining);
  return (
    <div
      className="order-eta-row"
      data-state={remaining.kind === "delayed" ? "delayed" : "progress"}
    >
      <span className="order-eta-label" id={labelId}>
        {t(bar.label)}
      </span>
      <span className="order-eta-note">
        {left}
        <span className="order-eta-deadline">
          {" · "}
          {t("order.eta.until", { time: time(bar.deadline) })}
        </span>
      </span>
      <div
        aria-labelledby={labelId}
        aria-valuemax={100}
        aria-valuemin={0}
        aria-valuenow={percent}
        aria-valuetext={left}
        className="order-eta-track"
        role="progressbar"
      >
        <span
          className="order-eta-fill"
          style={{ inlineSize: `${percent}%` }}
        />
      </div>
    </div>
  );
}

function remainingText(
  t: ReturnType<typeof useI18n>["t"],
  remaining: EtaRemaining,
): string {
  switch (remaining.kind) {
    case "delayed":
      return t("order.eta.delayed");
    case "lessThanMinute":
      return t("order.eta.lessThanMinute");
    case "minutes":
      return t("order.eta.remaining", { minutes: remaining.minutes });
  }
}
