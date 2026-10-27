import type { ReactNode } from "react";

import { useI18n } from "../../shared/i18n/i18n";
import { Button, Illustration } from "../../shared/ui";
import { CancelOrder } from "./cancel-order";
import { CourierMap } from "./courier-map";
import { activeStepEta } from "./fulfillment-eta";
import { HeaderEta, StepEtaBar } from "./order-eta";
import {
  endPresentation,
  milestoneLabel,
  progressSteps,
  showsCourierAssigned,
  statusPresentation,
} from "./order-presentation";
import { StatusIcon } from "./status-icon";
import { useOrderUpdates, type ViewedOrder } from "./use-order-updates";
import "./view-order.css";

type Money = ViewedOrder["total"];

function useMoney() {
  const { formatters } = useI18n();
  // Intl.NumberFormat formats the decimal string exactly, without floats.
  return (money: Money) =>
    formatters
      .currency(money.currency)
      .format(money.amount as Intl.StringNumericLiteral);
}

// Status, progress, cancellation, items and history of one order.
export function OrderDetails({ orderId }: { orderId: string }) {
  const { t } = useI18n();
  const { order, failed, refresh } = useOrderUpdates(orderId);

  if (order === undefined && failed) {
    return (
      <OrderMessage
        action={<Button onClick={refresh}>{t("order.error.retry")}</Button>}
        detail={t("order.error.detail")}
        title={t("order.error.title")}
      />
    );
  }
  if (order === undefined) return <OrderSkeleton />;
  if (order === null) {
    return (
      <OrderMessage
        detail={t("order.notFound.detail")}
        title={t("order.notFound.title")}
      />
    );
  }

  return <OrderView failed={failed} onRefresh={refresh} order={order} />;
}

function OrderView({
  order,
  failed,
  onRefresh,
}: {
  order: ViewedOrder;
  failed: boolean;
  onRefresh: () => void;
}) {
  const { t, formatters } = useI18n();
  const money = useMoney();
  const status = statusPresentation[order.status];
  const time = (value: string) => formatters.time.format(Date.parse(value));

  return (
    <article aria-labelledby="order-title" className="order-page">
      <header className="order-header">
        <div
          className="order-status"
          data-final={status.final}
          data-status={order.status}
        >
          <span className="order-status-icon">
            <StatusIcon name={status.icon} />
          </span>
          <div>
            <h3 id="order-title">{t(status.label)}</h3>
            <p>{t(status.detail)}</p>
            <HeaderEta order={order} />
            <p className="order-placed">
              {t("order.placedAt", { time: time(order.placedAt) })}
            </p>
          </div>
        </div>
        <p aria-live="polite" className="sr-only">
          {t(status.label)}
        </p>
      </header>

      {failed ? (
        <div className="order-stale" role="status">
          <p>{t("order.stale")}</p>
          <Button onClick={onRefresh} size="sm" variant="outline">
            {t("order.refresh")}
          </Button>
        </div>
      ) : null}

      <div className="order-layout">
        <div className="order-now">
          {order.tracking && !status.final ? (
            <CourierMap
              pickedUp={order.status === "PICKED_UP"}
              tracking={order.tracking}
            />
          ) : order.status === "DELIVERED" ? (
            <Illustration name="order-delivered" />
          ) : order.status === "PICKED_UP" ? (
            <Illustration name="on-the-way" />
          ) : null}
          {status.step === null ? (
            <EndNotice order={order} />
          ) : (
            <OrderProgress current={status.step} order={order} />
          )}

          {status.final ? null : (
            <CancelOrder
              availableUntil={order.cancellation?.availableUntil ?? null}
              onRequested={onRefresh}
              orderId={order.id}
            />
          )}
        </div>

        <section
          aria-labelledby="order-summary-title"
          className="order-card order-summary"
        >
          <h4 id="order-summary-title">{t("order.summary")}</h4>
          <p className="order-restaurant">{order.restaurant.name}</p>
          {order.priority === "PREMIUM" ? (
            // FR-654: priority, without a promised time.
            <p className="order-premium">{t("order.premium")}</p>
          ) : null}
          <ul className="order-items">
            {order.items.map((item) => (
              <li key={item.menuItem.id}>
                <span>
                  {item.menuItem.name || t("order.unnamedItem")}{" "}
                  <span className="order-quantity">
                    {t("order.quantity", { count: item.quantity })}
                  </span>
                </span>
                <span>{money(item.lineTotal)}</span>
              </li>
            ))}
          </ul>
          <dl className="order-facts">
            {order.premiumFee ? (
              <div>
                <dt>{t("order.premiumFee")}</dt>
                <dd data-testid="order-premium-fee">
                  {money(order.premiumFee)}
                </dd>
              </div>
            ) : null}
            <div className="order-total">
              <dt>{t("order.total")}</dt>
              <dd data-testid="order-total">{money(order.total)}</dd>
            </div>
          </dl>
        </section>

        <section
          aria-labelledby="order-history-title"
          className="order-card order-history-card"
        >
          <h4 id="order-history-title">{t("order.milestones")}</h4>
          <ol className="order-history">
            {order.milestones.map((milestone) => (
              <li key={`${milestone.kind}-${milestone.occurredAt}`}>
                <time dateTime={milestone.occurredAt}>
                  {time(milestone.occurredAt)}
                </time>
                <span>{t(milestoneLabel[milestone.kind])}</span>
              </li>
            ))}
          </ol>
        </section>
      </div>
    </article>
  );
}

function OrderProgress({
  order,
  current,
}: {
  order: ViewedOrder;
  current: number;
}) {
  const { t } = useI18n();
  const courierAssigned = showsCourierAssigned(order.status, order.milestones);
  // Only the active step counts towards its deadline.
  const eta = activeStepEta(order);

  return (
    <section aria-labelledby="order-progress-title" className="order-card">
      <h4 className="sr-only" id="order-progress-title">
        {t("order.progress")}
      </h4>
      <ol className="order-progress">
        {progressSteps.map((step, index) => {
          const state =
            index < current
              ? "done"
              : index === current
                ? "current"
                : "upcoming";
          return (
            <li
              aria-current={state === "current" ? "step" : undefined}
              data-state={state}
              key={step}
            >
              <span aria-hidden="true" className="order-progress-marker" />
              <span className="order-progress-label">
                {t(statusPresentation[step].label)}
                {state === "done" ? (
                  <span className="sr-only">, {t("order.stepDone")}</span>
                ) : null}
                {state === "current" ? (
                  <span className="sr-only">, {t("order.stepCurrent")}</span>
                ) : null}
              </span>
              {state === "current" && courierAssigned ? (
                <span className="order-courier-note">
                  {order.courier
                    ? t("order.courierNamed", { name: order.courier.name })
                    : t("order.courierAssigned")}
                </span>
              ) : null}
              {state === "current" && eta !== null ? (
                <StepEtaBar eta={eta} key={eta.stage} />
              ) : null}
            </li>
          );
        })}
      </ol>
    </section>
  );
}

function EndNotice({ order }: { order: ViewedOrder }) {
  const { t } = useI18n();
  const end = endPresentation(order.endReason);
  return (
    <section className="order-card order-end" data-status={order.status}>
      <h4>{t(end.title)}</h4>
      <p>{t(end.detail)}</p>
    </section>
  );
}

function OrderMessage({
  title,
  detail,
  action,
}: {
  title: string;
  detail: string;
  action?: ReactNode;
}) {
  return (
    <section
      aria-labelledby="order-message-title"
      className="order-page order-message"
    >
      <Illustration name="empty-mailbox" />
      <h3 id="order-message-title">{title}</h3>
      <p>{detail}</p>
      {action}
    </section>
  );
}

function OrderSkeleton() {
  const { t } = useI18n();
  return (
    <section
      aria-busy="true"
      aria-labelledby="order-loading"
      className="order-page"
    >
      <h3 className="sr-only" id="order-loading">
        {t("order.loading")}
      </h3>
      <div aria-hidden="true" className="order-skeleton">
        <span className="order-skeleton-line order-skeleton-title" />
        <span className="order-skeleton-line" />
        <span className="order-skeleton-block" />
        <span className="order-skeleton-block order-skeleton-tall" />
      </div>
    </section>
  );
}
