import { AnimatePresence, m } from "motion/react";
import { useRef, useState } from "react";
import { useMutation, useQuery, useSubscription } from "urql";

import { EtaBar, type EtaSpan, type RemainingCopy } from "../../shared/eta";
import {
  ApplyKitchenActionDocument,
  BoardKitchenChangedDocument,
  BoardKitchenDocument,
  type BoardKitchenQuery,
  BoardOwnedRestaurantsDocument,
  type KitchenAction,
  type KitchenOrderStage,
  StartNextPreparationDocument,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { AsRole } from "../../shared/session/roles";
import { Button } from "../../shared/ui";
import { RestaurantOpening } from "../manage-restaurant";
import { useDragSource } from "./board-dnd";
import { CookPoolPanel } from "./cook-pool";
import { useCourierOf } from "./courier-pool";
import { orderCardId } from "./order-card-id";
import { useLatest } from "./use-latest";

type KitchenOrder = NonNullable<BoardKitchenQuery["kitchen"]>["orders"][number];

const stageLabel: Record<KitchenOrderStage, MessageKey> = {
  RECEIVED: "board.kitchen.stage.RECEIVED",
  QUEUED: "board.kitchen.stage.QUEUED",
  PREPARING: "board.kitchen.stage.PREPARING",
  ASSEMBLED: "board.kitchen.stage.ASSEMBLED",
  READY: "board.kitchen.stage.READY",
};

// The next command of the kitchen at each stage. A QUEUED order has none:
// the kitchen takes the head of the queue (FR-655); READY waits for the
// courier.
const nextAction: Record<
  KitchenOrderStage,
  { action: KitchenAction; label: MessageKey } | null
> = {
  RECEIVED: { action: "ACCEPT", label: "board.kitchen.action.ACCEPT" },
  QUEUED: null,
  PREPARING: {
    action: "MARK_ASSEMBLED",
    label: "board.kitchen.action.MARK_ASSEMBLED",
  },
  ASSEMBLED: { action: "MARK_READY", label: "board.kitchen.action.MARK_READY" },
  READY: null,
};

// Stages that cook towards expectedReadyAt; READY is done, RECEIVED is not
// accepted yet.
const cookingStages = new Set<KitchenOrderStage>([
  "QUEUED",
  "PREPARING",
  "ASSEMBLED",
]);

// The kitchen says how soon the order is ready, short: «Готово через ~6 мин».
const kitchenCopy: RemainingCopy = {
  minutes: "board.kitchen.eta.minutes",
  countdown: "board.kitchen.eta.countdown",
};

// Cooking of an accepted order, from acceptance to the expected readiness;
// none without both moments.
function cookingSpan(order: KitchenOrder): EtaSpan | null {
  if (!cookingStages.has(order.stage)) return null;
  if (order.acceptedAt === null || order.expectedReadyAt === null) return null;
  return {
    startedAt: Date.parse(order.acceptedAt),
    deadline: Date.parse(order.expectedReadyAt),
  };
}

// Kitchen of the restaurant the board looks at (the owner's first one until
// then): the owner opens it, the kitchen staff moves its orders.
export function KitchenColumn({
  preferredRestaurantId,
  selectedOrderId,
  onOpenOrder,
}: {
  preferredRestaurantId: string | null;
  selectedOrderId: string | null;
  onOpenOrder: (orderId: string) => void;
}) {
  const { t } = useI18n();
  return (
    <AsRole as="owner">
      <OwnedRestaurants>
        {(restaurants) => {
          const ids = restaurants.map((restaurant) => restaurant.id);
          // Never another restaurant's kitchen in place of the picked one.
          if (preferredRestaurantId && !ids.includes(preferredRestaurantId)) {
            return <p className="board-empty">{t("board.kitchen.notOwned")}</p>;
          }
          if (ids.length === 0) {
            return <p className="board-empty">{t("board.kitchen.none")}</p>;
          }
          const restaurant = restaurants.find(
            (r) => r.id === preferredRestaurantId,
          );
          if (!restaurant) {
            return <p className="board-empty">{t("board.kitchen.pick")}</p>;
          }
          const restaurantId = restaurant.id;
          return (
            <>
              <RestaurantOpening key={restaurantId} restaurant={restaurant} />
              <AsRole as="kitchen">
                <KitchenOrders
                  onOpenOrder={onOpenOrder}
                  restaurantId={restaurantId}
                  selectedOrderId={selectedOrderId}
                />
              </AsRole>
            </>
          );
        }}
      </OwnedRestaurants>
    </AsRole>
  );
}

type KitchenRestaurant = Readonly<{
  id: string;
  name: string;
  acceptingOrders: boolean;
}>;

// Restaurants of the signed-in owner; nothing until the list loads, so the
// kitchen never claims a restaurant is someone else's while it is loading. A
// failed load is said and retried by hand.
function OwnedRestaurants({
  children,
}: {
  children: (restaurants: readonly KitchenRestaurant[]) => React.ReactNode;
}) {
  const { t } = useI18n();
  // Opening changes come with the kitchen board of the chosen restaurant;
  // the opening mutations refresh this list through the urql document cache.
  const [{ data, error }, reload] = useQuery({
    query: BoardOwnedRestaurantsDocument,
  });
  if (error && !data) {
    return (
      <div className="board-empty-state">
        <p className="board-error" role="alert">
          {t("board.kitchen.restaurantsError")}
        </p>
        <Button
          onClick={() => reload({ requestPolicy: "network-only" })}
          size="sm"
          variant="outline"
        >
          {t("restaurants.retry")}
        </Button>
      </div>
    );
  }
  if (!data) return <p className="board-empty">{t("board.loading")}</p>;
  return <>{children(data.myRestaurants.edges.map((edge) => edge.node))}</>;
}

function KitchenOrders({
  restaurantId,
  selectedOrderId,
  onOpenOrder,
}: {
  restaurantId: string;
  selectedOrderId: string | null;
  onOpenOrder: (orderId: string) => void;
}) {
  const { t, formatters } = useI18n();
  const [{ data, error }] = useQuery({
    query: BoardKitchenDocument,
    variables: { restaurantId },
  });
  const [{ data: pushed }] = useSubscription({
    query: BoardKitchenChangedDocument,
    variables: { restaurantId },
    pause: data?.kitchen === null,
  });
  const kitchen = useLatest(data?.kitchen, pushed?.kitchenChanged);

  if (!data && error) {
    return (
      <p className="board-error" role="alert">
        {t("board.kitchen.error")}
      </p>
    );
  }
  if (!data) return <p className="board-empty">{t("board.loading")}</p>;
  if (!kitchen) {
    return <p className="board-empty">{t("board.kitchen.notPermitted")}</p>;
  }
  const orderTimes = new Map(
    kitchen.orders.map((order) => [
      order.id,
      formatters.time.format(Date.parse(order.receivedAt)),
    ]),
  );
  const pool = (
    <CookPoolPanel
      orderTimes={orderTimes}
      pool={kitchen.cookPool}
      restaurantId={restaurantId}
    />
  );
  if (kitchen.orders.length === 0) {
    return (
      <>
        {pool}
        <p className="board-empty">{t("board.kitchen.empty")}</p>
      </>
    );
  }
  return (
    <>
      {pool}
      <TakeNext
        queued={kitchen.orders.some((order) => order.stage === "QUEUED")}
        restaurantId={restaurantId}
      />
      <ul aria-label={t("board.kitchen.orders")} className="board-cards">
        {/* Orders arrive with kitchenChanged: new ones slide in, finished
          ones fade out, and the rest move to their new place. */}
        <AnimatePresence initial={false}>
          {kitchen.orders.map((order) => (
            <m.li
              key={order.id}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, scale: 0.96 }}
              initial={{ opacity: 0, y: -8 }}
              layout
              layoutId={orderCardId("kitchen", order.orderId)}
              transition={{ duration: 0.2, ease: "easeOut" }}
            >
              <KitchenOrderCard
                onOpen={() => onOpenOrder(order.orderId)}
                order={order}
                restaurantId={restaurantId}
                selected={order.orderId === selectedOrderId}
              />
            </m.li>
          ))}
        </AnimatePresence>
      </ul>
    </>
  );
}

// Starts the head of the Kitchen Queue (FR-655): premium orders first, the
// kitchen does not choose. One request id per attempt, kept for a manual
// retry after a failed send so the retry starts no other order.
function TakeNext({
  restaurantId,
  queued,
}: {
  restaurantId: string;
  queued: boolean;
}) {
  const { t } = useI18n();
  const [{ fetching }, start] = useMutation(StartNextPreparationDocument);
  const [problem, setProblem] = useState<MessageKey | null>(null);
  const attempt = useRef<string | null>(null);

  async function takeNext() {
    setProblem(null);
    attempt.current ??= crypto.randomUUID();
    const result = await start({ restaurantId, requestId: attempt.current });
    const payload = result.data?.startNextPreparation;
    if (result.error || !payload) {
      setProblem("board.actionFailed");
      return;
    }
    attempt.current = null;
    if (payload.problem === "NO_COOK_AVAILABLE") {
      setProblem("board.kitchen.noCook");
    } else if (payload.problem) setProblem("board.kitchen.conflict");
  }

  return (
    <div className="board-take-next">
      <Button
        disabled={!queued}
        isPending={fetching}
        onClick={takeNext}
        size="sm"
      >
        {t("board.kitchen.takeNext")}
      </Button>
      {problem ? (
        <p className="board-error" role="alert">
          {t(problem)}
        </p>
      ) : null}
    </div>
  );
}

function KitchenOrderCard({
  order,
  restaurantId,
  selected,
  onOpen,
}: {
  order: KitchenOrder;
  restaurantId: string;
  selected: boolean;
  onOpen: () => void;
}) {
  const { t, formatters } = useI18n();
  const [{ fetching }, apply] = useMutation(ApplyKitchenActionDocument);
  const [problem, setProblem] = useState<MessageKey | null>(null);
  const next = nextAction[order.stage];
  const courier = useCourierOf(order.orderId);
  const cooking = cookingSpan(order);
  const receivedTime = formatters.time.format(Date.parse(order.receivedAt));

  async function send(action: KitchenAction) {
    setProblem(null);
    const result = await apply({
      input: { restaurantId, kitchenOrderId: order.id, action },
    });
    const payload = result.data?.applyKitchenAction;
    if (result.error || !payload) setProblem("board.actionFailed");
    else if (payload.problem === "NO_COOK_AVAILABLE")
      setProblem("board.kitchen.noCook");
    else if (payload.problem) setProblem("board.kitchen.conflict");
  }

  // An accepted order has a delivery, so the courier can take it.
  const drag = useDragSource(
    order.stage === "RECEIVED"
      ? null
      : { kind: "order", orderId: order.orderId },
  );

  return (
    <article
      className="board-card"
      data-selected={selected}
      data-stage={order.stage}
      {...drag}
    >
      <button className="board-card-head" onClick={onOpen} type="button">
        <span className="board-card-title">
          {t("board.kitchen.order", { time: receivedTime })}
        </span>
        <span className="board-badge">{t(stageLabel[order.stage])}</span>
        {order.priority === "PREMIUM" ? (
          <span className="board-badge" data-priority="premium">
            {t("board.kitchen.premium")}
          </span>
        ) : null}
      </button>
      <ul className="board-card-items">
        {order.items.map((item, index) => (
          // Lines have no id and never reorder within an order.
          // biome-ignore lint/suspicious/noArrayIndexKey: stable order lines
          <li key={`${item.name}-${index}`}>
            {item.name || t("order.unnamedItem")} ×{item.quantity}
          </li>
        ))}
      </ul>
      {order.cook || courier ? (
        <p className="board-card-people">
          {order.cook ? (
            <span>{t("board.kitchen.cook", { name: order.cook.name })}</span>
          ) : null}
          {courier ? (
            <span>
              {t(
                courier.delivering
                  ? "board.kitchen.courier"
                  : "board.kitchen.courierOffered",
                { name: courier.name },
              )}
            </span>
          ) : null}
        </p>
      ) : null}
      {cooking === null ? null : (
        // Counts locally each second while mounted; no requests (ADR 36).
        <EtaBar
          copy={kitchenCopy}
          label={t("board.kitchen.eta", { time: receivedTime })}
          span={cooking}
          stage="cooking"
        />
      )}
      <div className="board-card-actions">
        {next ? (
          <Button
            isPending={fetching}
            onClick={() => send(next.action)}
            size="sm"
          >
            {t(next.label)}
          </Button>
        ) : (
          <span className="board-card-wait">
            {t(
              order.stage === "QUEUED"
                ? "board.kitchen.waitQueue"
                : "board.kitchen.waitCourier",
            )}
          </span>
        )}
        {order.stage === "RECEIVED" ? (
          <Button
            disabled={fetching}
            onClick={() => send("REJECT")}
            size="sm"
            variant="ghost"
          >
            {t("board.kitchen.action.REJECT")}
          </Button>
        ) : null}
      </div>
      {problem ? (
        <p className="board-error" role="alert">
          {t(problem)}
        </p>
      ) : null}
    </article>
  );
}
