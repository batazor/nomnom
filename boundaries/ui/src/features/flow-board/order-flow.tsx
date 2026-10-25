import { m } from "motion/react";
import { useEffect, useState } from "react";
import { useQuery, useSubscription } from "urql";

import {
  BoardOrderChangedDocument,
  BoardOrderStatusDocument,
  type OrderStatus,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { Button } from "../../shared/ui";
import { orderCardId } from "./order-card-id";
import { useLatest } from "./use-latest";

// Steps of a successful order and the column whose action leads to each.
const flowSteps: readonly {
  status: OrderStatus;
  label: MessageKey;
  actor: MessageKey;
}[] = [
  { status: "RECEIVED", label: "board.step.received", actor: "board.customer" },
  {
    status: "CONFIRMED",
    label: "board.step.confirmed",
    actor: "board.kitchen",
  },
  {
    status: "PREPARING",
    label: "board.step.preparing",
    actor: "board.kitchen",
  },
  {
    status: "READY_FOR_PICKUP",
    label: "board.step.ready",
    actor: "board.kitchen",
  },
  { status: "PICKED_UP", label: "board.step.pickedUp", actor: "board.courier" },
  {
    status: "DELIVERED",
    label: "board.step.delivered",
    actor: "board.courier",
  },
];

const finalStatuses = new Set<OrderStatus>([
  "DELIVERED",
  "CANCELLED",
  "FAILED",
]);

// The order, undefined while it loads, null when BFF does not know it (any
// more): mock orders live only as long as the page.
function useBoardOrder(orderId: string) {
  const [{ data }] = useQuery({
    query: BoardOrderStatusDocument,
    variables: { id: orderId },
  });
  const loaded = data === undefined ? undefined : data.order;
  // Final as soon as the query or an event says so: then the stream closes.
  const [final, setFinal] = useState(false);
  const [{ data: pushed }] = useSubscription({
    query: BoardOrderChangedDocument,
    variables: { id: orderId },
    pause: !loaded || final,
  });
  const order = useLatest(loaded, pushed?.orderChanged);
  useEffect(() => {
    if (order && finalStatuses.has(order.status)) setFinal(true);
  }, [order]);
  return data === undefined ? undefined : order;
}

// Orders placed from the board as a strip of chips; the chosen one is
// highlighted in every column.
export function OrderStrip({
  orderIds,
  selectedId,
  onSelect,
  onMissing,
}: {
  orderIds: readonly string[];
  selectedId: string | null;
  onSelect: (orderId: string) => void;
  onMissing: (orderId: string) => void;
}) {
  const { t } = useI18n();
  if (orderIds.length === 0) {
    return <p className="board-strip-empty">{t("board.orders.empty")}</p>;
  }
  return (
    <ul aria-label={t("board.orders.title")} className="board-strip">
      {orderIds.map((orderId) => (
        <li key={orderId}>
          <OrderChip
            onMissing={onMissing}
            onSelect={onSelect}
            orderId={orderId}
            selected={orderId === selectedId}
          />
        </li>
      ))}
    </ul>
  );
}

function OrderChip({
  orderId,
  selected,
  onSelect,
  onMissing,
}: {
  orderId: string;
  selected: boolean;
  onSelect: (orderId: string) => void;
  onMissing: (orderId: string) => void;
}) {
  const { t, formatters } = useI18n();
  const order = useBoardOrder(orderId);
  useEffect(() => {
    if (order === null) onMissing(orderId);
  }, [order, orderId, onMissing]);
  if (order === null) return null;
  return (
    <button
      aria-pressed={selected}
      className="board-chip"
      data-status={order?.status}
      onClick={() => onSelect(orderId)}
      type="button"
    >
      {/* The highlight slides from the old chip to the new one. */}
      {selected ? (
        <m.span
          aria-hidden="true"
          className="board-chip-selected"
          layoutId="board-chip-selected"
          transition={{ type: "spring", bounce: 0.2, duration: 0.35 }}
        />
      ) : null}
      <span className="board-chip-name">
        {order?.restaurant.name ?? t("board.orders.loading")}
      </span>
      <span className="board-chip-meta">
        {order
          ? `${formatters.time.format(Date.parse(order.placedAt))} · ${t(statusLabel(order.status))}`
          : ""}
      </span>
    </button>
  );
}

function statusLabel(status: OrderStatus): MessageKey {
  return (
    flowSteps.find((step) => step.status === status)?.label ??
    (status === "CANCELLED" ? "board.step.cancelled" : "board.step.failed")
  );
}

// Where the chosen order is in the flow and which column acts next.
export function OrderTimeline({
  orderId,
  onDetails,
}: {
  orderId: string;
  onDetails: () => void;
}) {
  const { t } = useI18n();
  const order = useBoardOrder(orderId);
  if (order === null) return null;
  if (!order) {
    return <p className="board-timeline-empty">{t("board.orders.loading")}</p>;
  }
  const reached = flowSteps.findIndex((step) => step.status === order.status);
  const ended = order.status === "CANCELLED" || order.status === "FAILED";
  const next = reached >= 0 ? flowSteps[reached + 1] : undefined;

  return (
    <m.section
      animate={{ opacity: 1, y: 0 }}
      aria-label={t("board.timeline.title")}
      className="board-timeline"
      initial={{ opacity: 0, y: -6 }}
      layoutId={orderCardId("timeline", orderId)}
    >
      <ol className="board-steps">
        {flowSteps.map((step, index) => (
          <li
            aria-current={index === reached ? "step" : undefined}
            data-state={
              ended
                ? "ended"
                : index < reached
                  ? "done"
                  : index === reached
                    ? "current"
                    : "todo"
            }
            key={step.status}
          >
            <span className="board-step-label">{t(step.label)}</span>
            <span className="board-step-actor">{t(step.actor)}</span>
          </li>
        ))}
      </ol>
      <div className="board-timeline-footer">
        <p aria-live="polite" className="board-next">
          {ended
            ? t(statusLabel(order.status))
            : next
              ? t("board.next", { actor: t(next.actor), step: t(next.label) })
              : t("board.done")}
        </p>
        <Button onClick={onDetails} size="sm" variant="outline">
          {t("board.details")}
        </Button>
      </div>
    </m.section>
  );
}
