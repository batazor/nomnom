import { m } from "motion/react";
import { useState } from "react";
import { useMutation, useQuery } from "urql";

import {
  AcceptDeliveryOfferDocument,
  ApplyCourierDeliveryActionDocument,
  BoardCourierDocument,
  type CourierAvailability,
  type CourierProblemCode,
  SetCourierAvailabilityDocument,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { Button } from "../../shared/ui";
import { useBoardRefresh, usePolling } from "./use-polling";

const availabilityLabel: Record<CourierAvailability, MessageKey> = {
  AVAILABLE: "board.courier.AVAILABLE",
  BUSY: "board.courier.BUSY",
  UNAVAILABLE: "board.courier.UNAVAILABLE",
};

const problemMessage: Record<CourierProblemCode, MessageKey> = {
  OFFER_NOT_AVAILABLE: "board.courier.problem.OFFER_NOT_AVAILABLE",
  DELIVERY_STAGE_CONFLICT: "board.courier.problem.DELIVERY_STAGE_CONFLICT",
};

type Outcome = Readonly<{
  error?: unknown;
  problem?: CourierProblemCode | null;
}>;

// Work of the demo courier: going online, offers and the current delivery.
export function CourierColumn({
  selectedOrderId,
  onSelectOrder,
}: {
  selectedOrderId: string | null;
  onSelectOrder: (orderId: string) => void;
}) {
  const { t } = useI18n();
  const [{ data, error }, reexecute] = useQuery({
    query: BoardCourierDocument,
  });
  usePolling(() => reexecute({ requestPolicy: "network-only" }));
  const [availability, setAvailability] = useMutation(
    SetCourierAvailabilityDocument,
  );
  const [accepting, acceptOffer] = useMutation(AcceptDeliveryOfferDocument);
  const [acting, applyAction] = useMutation(ApplyCourierDeliveryActionDocument);
  const [problem, setProblem] = useState<MessageKey | null>(null);
  const refreshBoard = useBoardRefresh();

  function settle(outcome: Outcome) {
    if (outcome.error) setProblem("board.actionFailed");
    else if (outcome.problem) setProblem(problemMessage[outcome.problem]);
    else setProblem(null);
    refreshBoard();
  }

  if (!data && error) {
    return (
      <p className="board-error" role="alert">
        {t("board.courier.error")}
      </p>
    );
  }
  if (!data) return <p className="board-empty">{t("board.loading")}</p>;
  const courier = data.courier;
  if (!courier) {
    return <p className="board-empty">{t("board.courier.notRegistered")}</p>;
  }
  const delivery = courier.currentDelivery;

  return (
    <div className="board-courier">
      <div className="board-courier-state">
        <p className="board-state" data-availability={courier.availability}>
          <span aria-hidden="true" className="board-state-dot" />
          {t(availabilityLabel[courier.availability])}
        </p>
        {courier.availability === "BUSY" ? null : (
          <Button
            isPending={availability.fetching}
            onPress={async () => {
              const result = await setAvailability({
                availability:
                  courier.availability === "AVAILABLE"
                    ? "UNAVAILABLE"
                    : "AVAILABLE",
              });
              settle({
                error: result.error,
                problem: result.data?.setCourierAvailability.problem,
              });
            }}
            size="sm"
            variant={
              courier.availability === "AVAILABLE" ? "outline" : "primary"
            }
          >
            {courier.availability === "AVAILABLE"
              ? t("board.courier.goOffline")
              : t("board.courier.goOnline")}
          </Button>
        )}
      </div>

      {delivery ? (
        <article
          className="board-card"
          data-selected={delivery.orderId === selectedOrderId}
          data-stage={delivery.stage}
        >
          <button
            className="board-card-head"
            onClick={() => onSelectOrder(delivery.orderId)}
            type="button"
          >
            <span className="board-card-title">
              {t("board.courier.current")}
            </span>
            <span className="board-badge">
              {t(
                delivery.stage === "ASSIGNED"
                  ? "board.courier.stage.ASSIGNED"
                  : "board.courier.stage.PICKED_UP",
              )}
            </span>
          </button>
          <Route from={delivery.pickupAddress} to={delivery.dropoffAddress} />
          <div className="board-card-actions">
            <Button
              isPending={acting.fetching}
              onPress={async () => {
                const result = await applyAction({
                  deliveryId: delivery.id,
                  action:
                    delivery.stage === "ASSIGNED"
                      ? "CONFIRM_PICKUP"
                      : "CONFIRM_DELIVERY",
                });
                settle({
                  error: result.error,
                  problem: result.data?.applyCourierDeliveryAction.problem,
                });
              }}
              size="sm"
            >
              {delivery.stage === "ASSIGNED"
                ? t("board.courier.confirmPickup")
                : t("board.courier.confirmDelivery")}
            </Button>
          </div>
        </article>
      ) : null}

      {courier.offers.length > 0 ? (
        <ul aria-label={t("board.courier.offers")} className="board-cards">
          {courier.offers.map((offer) => (
            <li key={offer.id}>
              <article className="board-card" data-stage="OFFER">
                <div className="board-card-head">
                  <span className="board-card-title">
                    {t("board.courier.offer")}
                  </span>
                  <OfferCountdown expiresAt={offer.expiresAt} />
                </div>
                <Route from={offer.pickupAddress} to={offer.dropoffAddress} />
                <div className="board-card-actions">
                  <Button
                    isPending={accepting.fetching}
                    onPress={async () => {
                      const result = await acceptOffer({ offerId: offer.id });
                      settle({
                        error: result.error,
                        problem: result.data?.acceptDeliveryOffer.problem,
                      });
                    }}
                    size="sm"
                  >
                    {t("board.courier.accept")}
                  </Button>
                </div>
              </article>
            </li>
          ))}
        </ul>
      ) : delivery ? null : (
        <p className="board-empty">
          {courier.availability === "AVAILABLE"
            ? t("board.courier.waiting")
            : t("board.courier.offline")}
        </p>
      )}

      {problem ? (
        <p className="board-error" role="alert">
          {t(problem)}
        </p>
      ) : null}
    </div>
  );
}

// A ring that empties until the offer expires. The remaining time is taken
// once per offer, so polling does not restart it.
function OfferCountdown({ expiresAt }: { expiresAt: string }) {
  const [seconds] = useState(() =>
    Math.max(0, (Date.parse(expiresAt) - Date.now()) / 1000),
  );
  return (
    <svg
      aria-hidden="true"
      className="board-countdown"
      fill="none"
      focusable="false"
      viewBox="0 0 24 24"
    >
      <circle className="board-countdown-track" cx="12" cy="12" r="9" />
      <m.circle
        animate={{ pathLength: 0 }}
        className="board-countdown-left"
        cx="12"
        cy="12"
        initial={{ pathLength: 1 }}
        r="9"
        transform="rotate(-90 12 12)"
        transition={{ duration: seconds, ease: "linear" }}
      />
    </svg>
  );
}

function Route({ from, to }: { from: string; to: string }) {
  const { t } = useI18n();
  return (
    <dl className="board-route">
      <div>
        <dt>{t("board.courier.from")}</dt>
        <dd>{from}</dd>
      </div>
      <div>
        <dt>{t("board.courier.to")}</dt>
        <dd>{to}</dd>
      </div>
    </dl>
  );
}
