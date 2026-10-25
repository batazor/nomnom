import { AnimatePresence, m } from "motion/react";
import { useState } from "react";
import { useMutation, useQuery, useSubscription } from "urql";

import {
  ApplyKitchenActionDocument,
  BoardKitchenChangedDocument,
  BoardKitchenDocument,
  type BoardKitchenQuery,
  BoardOwnedRestaurantsDocument,
  type KitchenAction,
  type KitchenOrderStage,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { AsRole } from "../../shared/session/roles";
import { Button } from "../../shared/ui";
import { RestaurantOpening } from "../manage-restaurant";
import { useDragSource } from "./board-dnd";
import { useLatest } from "./use-latest";

type KitchenOrder = NonNullable<BoardKitchenQuery["kitchen"]>["orders"][number];

const stageLabel: Record<KitchenOrderStage, MessageKey> = {
  RECEIVED: "board.kitchen.stage.RECEIVED",
  QUEUED: "board.kitchen.stage.QUEUED",
  PREPARING: "board.kitchen.stage.PREPARING",
  ASSEMBLED: "board.kitchen.stage.ASSEMBLED",
  READY: "board.kitchen.stage.READY",
};

// The next command of the kitchen at each stage; READY waits for the courier.
const nextAction: Record<
  KitchenOrderStage,
  { action: KitchenAction; label: MessageKey } | null
> = {
  RECEIVED: { action: "ACCEPT", label: "board.kitchen.action.ACCEPT" },
  QUEUED: {
    action: "START_PREPARATION",
    label: "board.kitchen.action.START_PREPARATION",
  },
  PREPARING: {
    action: "MARK_ASSEMBLED",
    label: "board.kitchen.action.MARK_ASSEMBLED",
  },
  ASSEMBLED: { action: "MARK_READY", label: "board.kitchen.action.MARK_READY" },
  READY: null,
};

// Kitchen of the restaurant the board looks at (the owner's first one until
// then): the owner opens it, the kitchen staff moves its orders.
export function KitchenColumn({
  preferredRestaurantId,
  selectedOrderId,
  onSelectOrder,
}: {
  preferredRestaurantId: string | null;
  selectedOrderId: string | null;
  onSelectOrder: (orderId: string) => void;
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
          const restaurantId = preferredRestaurantId ?? ids[0] ?? null;
          const restaurant = restaurants.find((r) => r.id === restaurantId);
          if (!restaurantId || !restaurant) {
            return <p className="board-empty">{t("board.kitchen.none")}</p>;
          }
          return (
            <>
              <RestaurantOpening key={restaurantId} restaurant={restaurant} />
              <AsRole as="kitchen">
                <KitchenOrders
                  onSelectOrder={onSelectOrder}
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
  onSelectOrder,
}: {
  restaurantId: string;
  selectedOrderId: string | null;
  onSelectOrder: (orderId: string) => void;
}) {
  const { t } = useI18n();
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
  if (kitchen.orders.length === 0) {
    return <p className="board-empty">{t("board.kitchen.empty")}</p>;
  }
  return (
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
            transition={{ duration: 0.2, ease: "easeOut" }}
          >
            <KitchenOrderCard
              onSelect={() => onSelectOrder(order.orderId)}
              order={order}
              restaurantId={restaurantId}
              selected={order.orderId === selectedOrderId}
            />
          </m.li>
        ))}
      </AnimatePresence>
    </ul>
  );
}

function KitchenOrderCard({
  order,
  restaurantId,
  selected,
  onSelect,
}: {
  order: KitchenOrder;
  restaurantId: string;
  selected: boolean;
  onSelect: () => void;
}) {
  const { t, formatters } = useI18n();
  const [{ fetching }, apply] = useMutation(ApplyKitchenActionDocument);
  const [problem, setProblem] = useState<MessageKey | null>(null);
  const next = nextAction[order.stage];

  async function send(action: KitchenAction) {
    setProblem(null);
    const result = await apply({
      input: { restaurantId, kitchenOrderId: order.id, action },
    });
    const payload = result.data?.applyKitchenAction;
    if (result.error || !payload) setProblem("board.actionFailed");
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
      <button className="board-card-head" onClick={onSelect} type="button">
        <span className="board-card-title">
          {t("board.kitchen.order", {
            time: formatters.time.format(Date.parse(order.receivedAt)),
          })}
        </span>
        <span className="board-badge">{t(stageLabel[order.stage])}</span>
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
            {t("board.kitchen.waitCourier")}
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
