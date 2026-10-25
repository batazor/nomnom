import { LayoutGroup, m } from "motion/react";
import { type ReactNode, useCallback, useState } from "react";
import { useClient, useQuery } from "urql";
import {
  boardUser,
  SignInButton,
  useSignInAvailable,
} from "../../shared/auth/sign-in";
import {
  BoardOrderStatusDocument,
  ViewRestaurantDocument,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { AsRole, useCanAct } from "../../shared/session/roles";
import { Button, ExpandedCard, HelpTip, Illustration } from "../../shared/ui";
import { RestaurantPicker } from "../browse-restaurants";
import { OrderDetails } from "../view-order";
import { RestaurantOrdering } from "../view-restaurant";
import { BoardDragProvider } from "./board-dnd";
import { useBoardOrders } from "./board-orders";
import { CourierColumn } from "./courier-column";
import { KitchenColumn } from "./kitchen-column";
import { type DetailsSource, orderCardId } from "./order-card-id";
import { OrderStrip, OrderTimeline } from "./order-flow";
import "./flow-board.css";

type Column = "customer" | "kitchen" | "courier";

const columnTitle: Record<Column, MessageKey> = {
  customer: "board.customer",
  kitchen: "board.kitchen",
  courier: "board.courier",
};

// The whole order flow on one screen: the customer orders, the kitchen cooks,
// the courier delivers, each column as its own user. Order details grow out of
// the card they were opened from. The three columns stay side by side on any
// screen.
export function FlowBoard() {
  return (
    <BoardDragProvider>
      <Board />
    </BoardDragProvider>
  );
}

function Board() {
  const { t } = useI18n();
  const signInAvailable = useSignInAvailable();
  const canAct = useCanAct();
  const orders = useBoardOrders();
  const [selectedOrderId, setSelectedOrderId] = useState<string | null>(null);
  const [customerRestaurantId, setCustomerRestaurantId] = useState<
    string | null
  >(null);
  const [details, setDetails] = useState<{
    orderId: string;
    from: DetailsSource;
  } | null>(null);
  const selectedRestaurantId = useOrderRestaurant(selectedOrderId);
  // The kitchen follows the latest restaurant the board looked at: the one
  // the customer picked or the one of the opened order.
  const [kitchenRestaurantId, setKitchenRestaurantId] = useState<string | null>(
    null,
  );
  const [seenOrderRestaurantId, setSeenOrderRestaurantId] =
    useState(selectedRestaurantId);
  if (selectedRestaurantId !== seenOrderRestaurantId) {
    setSeenOrderRestaurantId(selectedRestaurantId);
    if (selectedRestaurantId) setKitchenRestaurantId(selectedRestaurantId);
  }

  const { remove } = orders;
  const forget = useCallback(
    (orderId: string) => {
      remove(orderId);
      setSelectedOrderId((current) => (current === orderId ? null : current));
    },
    [remove],
  );

  function openDetails(orderId: string, from: DetailsSource) {
    setSelectedOrderId(orderId);
    setDetails({ orderId, from });
  }

  // The restaurant opens once its menu is in the cache: the header and the
  // dish icons then mount at once and fly out of the card, with no skeleton
  // in between. A failed read opens it too, to show its error.
  const client = useClient();
  async function openRestaurant(restaurantId: string) {
    await client
      .query(ViewRestaurantDocument, { id: restaurantId })
      .toPromise();
    setCustomerRestaurantId(restaurantId);
    setKitchenRestaurantId(restaurantId);
  }

  function placed(orderId: string) {
    orders.add(orderId);
    setSelectedOrderId(orderId);
  }

  return (
    <div className="board">
      <h1 className="sr-only">{t("board.title")}</h1>

      {/* Mocks need no sign-in; against BFF the board acts as one user. */}
      {signInAvailable ? (
        <div className="board-sign-in">
          <SignInButton role={boardUser} roleLabel={t("auth.role.demo")} />
        </div>
      ) : null}

      <LayoutGroup>
        <m.section
          aria-labelledby="board-orders-title"
          className="board-top"
          layout
        >
          <h2 className="board-top-title" id="board-orders-title">
            {t("board.orders.title")}
          </h2>
          <OrderStrip
            onMissing={forget}
            onSelect={setSelectedOrderId}
            orderIds={orders.ids}
            selectedId={selectedOrderId}
          />
          {selectedOrderId ? (
            <OrderTimeline
              key={selectedOrderId}
              onDetails={() => openDetails(selectedOrderId, "timeline")}
              orderId={selectedOrderId}
            />
          ) : null}
        </m.section>

        <m.div className="board-columns" layout="position">
          <BoardColumn column="customer">
            {/* The chosen restaurant card opens like an iOS folder: the card
              becomes the restaurant header and its dish icons fly to their
              places in the menu; going back folds them into the card again. */}
            {customerRestaurantId ? (
              <div className="board-restaurant" key={customerRestaurantId}>
                <Button
                  className="board-restaurant-back"
                  onClick={() => setCustomerRestaurantId(null)}
                  size="sm"
                  variant="ghost"
                >
                  {t("board.customer.back")}
                </Button>
                <RestaurantOrdering
                  onPlaced={placed}
                  restaurantId={customerRestaurantId}
                />
              </div>
            ) : (
              <>
                <RestaurantPicker
                  onSelect={(restaurantId) => void openRestaurant(restaurantId)}
                />
                <div className="board-empty-state">
                  <Illustration name="order-food" size="sm" />
                  <p className="board-empty">{t("board.customer.pick")}</p>
                </div>
              </>
            )}
          </BoardColumn>

          <BoardColumn column="kitchen">
            {canAct ? (
              <KitchenColumn
                onOpenOrder={(orderId) => openDetails(orderId, "kitchen")}
                preferredRestaurantId={kitchenRestaurantId}
                selectedOrderId={selectedOrderId}
              />
            ) : (
              <div className="board-empty-state">
                <Illustration name="take-out-boxes" size="sm" />
                <p className="board-empty">{t("board.session.neededSignIn")}</p>
              </div>
            )}
          </BoardColumn>

          <BoardColumn column="courier">
            {canAct ? (
              <AsRole as="courier">
                <CourierColumn
                  onOpenOrder={(orderId) => openDetails(orderId, "courier")}
                  selectedOrderId={selectedOrderId}
                />
              </AsRole>
            ) : (
              <div className="board-empty-state">
                <Illustration name="delivery-truck" size="sm" />
                <p className="board-empty">{t("board.session.neededSignIn")}</p>
              </div>
            )}
          </BoardColumn>
        </m.div>

        <ExpandedCard
          closeLabel={t("board.close")}
          layoutId={details ? orderCardId(details.from, details.orderId) : null}
          onClose={() => setDetails(null)}
          title={t("board.details")}
        >
          {details ? <OrderDetails orderId={details.orderId} /> : null}
        </ExpandedCard>
      </LayoutGroup>
    </div>
  );
}

function BoardColumn({
  column,
  children,
}: {
  column: Column;
  children: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <section
      aria-labelledby={`board-column-${column}-title`}
      className="board-column"
      data-column={column}
    >
      <header className="board-column-header">
        <h2 id={`board-column-${column}-title`}>{t(columnTitle[column])}</h2>
        <HelpTip label={t("help.about", { topic: t(columnTitle[column]) })}>
          {t(`board.${column}.lead` as MessageKey)}
        </HelpTip>
      </header>
      <div className="board-column-body">{children}</div>
    </section>
  );
}

// Restaurant of the chosen order, so the kitchen column follows it.
function useOrderRestaurant(orderId: string | null): string | null {
  const [{ data }] = useQuery({
    query: BoardOrderStatusDocument,
    variables: { id: orderId ?? "" },
    pause: !orderId,
  });
  return data?.order?.restaurant.id ?? null;
}
