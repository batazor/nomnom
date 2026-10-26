import {
  EtaBar,
  etaRemaining,
  type RemainingCopy,
  remainingText,
  useNow,
} from "../../shared/eta";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { type HeaderEtaKind, headerEta, type StepEta } from "./fulfillment-eta";
import type { ViewedOrder } from "./use-order-updates";

const headerMessage: Record<HeaderEtaKind, MessageKey> = {
  delivered: "order.eta.header.delivered",
  pickup: "order.eta.header.pickup",
  ready: "order.eta.header.ready",
};

// The order screen says the time left as ~N min or a bare m:ss.
const orderCopy: RemainingCopy = {
  minutes: "order.eta.minutes",
  countdown: "order.eta.countdown",
};

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
          {remainingText(t, remaining, orderCopy)}
        </span>
      ) : null}
    </p>
  );
}

// The inline bar of the active step, under its label.
export function StepEtaBar({ eta }: { eta: StepEta }) {
  const { t } = useI18n();
  return (
    <EtaBar
      className="order-step-eta"
      copy={orderCopy}
      label={t(eta.label)}
      span={eta}
      stage={eta.stage}
    />
  );
}
