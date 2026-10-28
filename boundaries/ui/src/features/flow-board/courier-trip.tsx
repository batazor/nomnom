import { m } from "motion/react";
import { useId, useState } from "react";
import { useQuery, useSubscription } from "urql";

import {
  BoardCourierTripChangedDocument,
  BoardCourierTripDocument,
  type CourierTripViewFragment,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { useLatest } from "./use-latest";

type Leg = Readonly<{
  label: MessageKey;
  startedAt: number;
  endsAt: number;
}>;

// The current leg of the trip: to the restaurant from the assignment until
// the expected pickup, then to the customer from the pickup until the
// expected delivery. null until the courier names the moment the leg ends.
function currentLeg(order: CourierTripViewFragment): Leg | null {
  const at = (kind: string) =>
    order.milestones.find((milestone) => milestone.kind === kind)?.occurredAt;
  const pickedUp = at("PICKED_UP");
  if (pickedUp) {
    return order.expectedDeliveredAt
      ? {
          label: "board.trip.toCustomer",
          startedAt: Date.parse(pickedUp),
          endsAt: Date.parse(order.expectedDeliveredAt),
        }
      : null;
  }
  const assigned = at("COURIER_ASSIGNED");
  return assigned && order.expectedPickupAt
    ? {
        label: "board.trip.toRestaurant",
        startedAt: Date.parse(assigned),
        endsAt: Date.parse(order.expectedPickupAt),
      }
    : null;
}

// Progress of the order a courier is delivering, under their row in the
// Courier Pool. The bar fills on its own until the expected moment, so
// nothing polls (ADR 36); hovering or focusing it tells how long is left.
export function CourierTrip({
  orderId,
  courierName,
}: {
  orderId: string;
  courierName: string;
}) {
  const { t, formatters } = useI18n();
  const [{ data }] = useQuery({
    query: BoardCourierTripDocument,
    variables: { id: orderId },
  });
  const [{ data: pushed }] = useSubscription({
    query: BoardCourierTripChangedDocument,
    variables: { id: orderId },
    pause: !data?.order,
  });
  const order = useLatest(data?.order ?? undefined, pushed?.orderChanged);
  const [open, setOpen] = useState(false);
  const tipId = useId();
  if (!order) return null;
  const leg = currentLeg(order);
  if (!leg) return null;

  const now = Date.now();
  const span = Math.max(leg.endsAt - leg.startedAt, 1);
  const done = Math.min(Math.max((now - leg.startedAt) / span, 0), 1);
  const leftMs = leg.endsAt - now;
  const minutes = Math.max(Math.ceil(Math.abs(leftMs) / 60_000), 1);
  const time = formatters.time.format(leg.endsAt);
  const late = leftMs < 0;

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: hovering the bar or its tooltip keeps it open for the mouse; keyboard users get it from the bar's focus.
    <span
      className="board-trip"
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => setOpen(false)}
    >
      <span
        aria-describedby={tipId}
        aria-label={t("board.trip.label", { name: courierName })}
        aria-valuemax={100}
        aria-valuemin={0}
        aria-valuenow={Math.round(done * 100)}
        className="board-trip-bar"
        data-late={late || undefined}
        onBlur={() => setOpen(false)}
        onFocus={() => setOpen(true)}
        onKeyDown={(event) => {
          if (event.key === "Escape") setOpen(false);
        }}
        role="progressbar"
        // biome-ignore lint/a11y/noNoninteractiveTabindex: the bar is focusable so keyboard users can read its tooltip.
        tabIndex={0}
      >
        {/* Keyed by the leg: a new leg starts from its own share. */}
        <m.span
          animate={{ scaleX: 1 }}
          className="board-trip-fill"
          initial={{ scaleX: done }}
          key={`${leg.label}-${leg.endsAt}`}
          transition={{ duration: Math.max(leftMs, 0) / 1000, ease: "linear" }}
        />
      </span>
      <span
        className="nomnom-help-panel board-trip-panel"
        data-closed={open ? undefined : true}
        id={tipId}
        role="tooltip"
      >
        {t(leg.label)}
        <br />
        {late
          ? t("board.trip.late", { minutes, time })
          : t("board.trip.left", { minutes, time })}
      </span>
    </span>
  );
}
