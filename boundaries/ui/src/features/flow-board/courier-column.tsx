import { AnimatePresence, m } from "motion/react";
import { useState } from "react";
import { useMutation, useQuery, useSubscription } from "urql";

import {
  AcceptDeliveryOfferDocument,
  ApplyCourierDeliveryActionDocument,
  BoardCourierChangedDocument,
  BoardCourierDocument,
  type CourierAvailability,
  type CourierProblemCode,
  SetCourierAvailabilityDocument,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { Button, Illustration } from "../../shared/ui";
import { CourierPoolPanel } from "./courier-pool";
import { orderCardId } from "./order-card-id";
import { useLatest } from "./use-latest";

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
  onOpenOrder,
}: {
  selectedOrderId: string | null;
  onOpenOrder: (orderId: string) => void;
}) {
  const { t } = useI18n();
  const [{ data, error }] = useQuery({
    query: BoardCourierDocument,
  });
  // Courier.id is the subject of the user: courierChanged pushes their work.
  const courierId = data?.courier?.id;
  const [{ data: pushed }] = useSubscription({
    query: BoardCourierChangedDocument,
    variables: { id: courierId ?? "" },
    pause: !courierId,
  });
  const courier = useLatest(data?.courier, pushed?.courierChanged);
  const [availability, setAvailability] = useMutation(
    SetCourierAvailabilityDocument,
  );
  const [accepting, acceptOffer] = useMutation(AcceptDeliveryOfferDocument);
  const [acting, applyAction] = useMutation(ApplyCourierDeliveryActionDocument);
  const [problem, setProblem] = useState<MessageKey | null>(null);

  function settle(outcome: Outcome) {
    if (outcome.error) setProblem("board.actionFailed");
    else if (outcome.problem) setProblem(problemMessage[outcome.problem]);
    else setProblem(null);
  }

  if (!data && error) {
    return (
      <>
        <CourierPoolPanel />
        <p className="board-error" role="alert">
          {t("board.courier.error")}
        </p>
      </>
    );
  }
  if (!data) return <p className="board-empty">{t("board.loading")}</p>;
  if (!courier) {
    return (
      <>
        <CourierPoolPanel />
        <p className="board-empty">{t("board.courier.notRegistered")}</p>
      </>
    );
  }
  const delivery = courier.currentDelivery;

  return (
    <div className="board-courier">
      <CourierPoolPanel />
      <div className="board-courier-state">
        <p className="board-state" data-availability={courier.availability}>
          <span aria-hidden="true" className="board-state-dot" />
          {t(availabilityLabel[courier.availability])}
        </p>
        {courier.availability === "BUSY" ? null : (
          <Button
            isPending={availability.fetching}
            onClick={async () => {
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
        <m.article
          className="board-card"
          data-selected={delivery.orderId === selectedOrderId}
          data-stage={delivery.stage}
          layoutId={orderCardId("courier", delivery.orderId)}
        >
          <button
            className="board-card-head"
            onClick={() => onOpenOrder(delivery.orderId)}
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
          <div className="board-card-actions">
            <Button
              isPending={acting.fetching}
              onClick={async () => {
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
        </m.article>
      ) : null}

      {courier.offers.length > 0 ? (
        <ul aria-label={t("board.courier.offers")} className="board-cards">
          <AnimatePresence initial={false}>
            {courier.offers.map((offer) => (
              <m.li
                key={offer.id}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, scale: 0.96 }}
                initial={{ opacity: 0, y: -8 }}
                layout
                transition={{ duration: 0.2, ease: "easeOut" }}
              >
                <article className="board-card" data-stage="OFFER">
                  <div className="board-card-head">
                    <span className="board-card-title">
                      {t("board.courier.offer")}
                    </span>
                    <OfferCountdown expiresAt={offer.expiresAt} />
                  </div>
                  <div className="board-card-actions">
                    <Button
                      isPending={accepting.fetching}
                      onClick={async () => {
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
              </m.li>
            ))}
          </AnimatePresence>
        </ul>
      ) : delivery ? null : (
        <div className="board-empty-state">
          <Illustration
            name={
              courier.availability === "AVAILABLE"
                ? "on-the-way"
                : "empty-mailbox"
            }
            size="sm"
          />
          <p className="board-empty">
            {courier.availability === "AVAILABLE"
              ? t("board.courier.waiting")
              : t("board.courier.offline")}
          </p>
        </div>
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
// once per offer, so a courierChanged update does not restart it.
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
