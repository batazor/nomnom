import { LayoutGroup, m } from "motion/react";
import { type ReactNode, useCallback, useState } from "react";
import { useClient, useQuery } from "urql";
import type { CourierPoolViewFragment } from "../../shared/graphql/generated/graphql";
import {
  BoardOrderStatusDocument,
  ViewRestaurantDocument,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { AsRole, useCanAct } from "../../shared/session/roles";
import { Button, ExpandedCard, HelpTip, Illustration } from "../../shared/ui";
import { RestaurantPicker } from "../browse-restaurants";
import { checkoutFormId } from "../place-order";
import { OrderDetails } from "../view-order";
import { RestaurantOrdering } from "../view-restaurant";
import { BoardDragProvider, useDragSource, useDropTarget } from "./board-dnd";
import { useBoardOrders } from "./board-orders";
import { CourierColumn } from "./courier-column";
import { CourierPoolProvider, CourierPoolSource } from "./courier-pool";
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
  const canAct = useCanAct();
  const orders = useBoardOrders();
  const [selectedOrderId, setSelectedOrderId] = useState<string | null>(null);
  const [customerRestaurantId, setCustomerRestaurantId] = useState<
    string | null
  >(null);
  // Courier Pool for dispatchers: the courier column and the kitchen cards.
  const [courierPool, setCourierPool] =
    useState<CourierPoolViewFragment | null>(null);
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

        {canAct ? (
          <AsRole as="courier">
            <CourierPoolSource onPool={setCourierPool} />
          </AsRole>
        ) : null}
        <CourierPoolProvider pool={canAct ? courierPool : null}>
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
                  <DraggableOrdering
                    onPlaced={placed}
                    restaurantId={customerRestaurantId}
                  />
                </div>
              ) : (
                <>
                  <RestaurantPicker
                    onSelect={(restaurantId) =>
                      void openRestaurant(restaurantId)
                    }
                  />
                  <div className="board-empty-state">
                    <Illustration name="order-food" size="sm" />
                    <p className="board-empty">{t("board.customer.pick")}</p>
                  </div>
                </>
              )}
            </BoardColumn>

            <KitchenDropColumn>
              {canAct ? (
                <KitchenColumn
                  onOpenOrder={(orderId) => openDetails(orderId, "kitchen")}
                  preferredRestaurantId={kitchenRestaurantId}
                  selectedOrderId={selectedOrderId}
                />
              ) : (
                <div className="board-empty-state">
                  <Illustration name="take-out-boxes" size="sm" />
                  <p className="board-empty">
                    {t("board.session.neededSignIn")}
                  </p>
                </div>
              )}
            </KitchenDropColumn>

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
                  <p className="board-empty">
                    {t("board.session.neededSignIn")}
                  </p>
                </div>
              )}
            </BoardColumn>
          </m.div>
        </CourierPoolProvider>

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

// The filled cart itself is the thing to drag: dropped on the kitchen it is
// checked out, the same as its checkout button.
function DraggableOrdering({
  restaurantId,
  onPlaced,
}: {
  restaurantId: string;
  onPlaced: (orderId: string) => void;
}) {
  const cartDrag = useDragSource({
    kind: "cart",
    submit: () =>
      document
        .querySelector<HTMLFormElement>(`#${checkoutFormId}`)
        ?.requestSubmit(),
  });
  return (
    <RestaurantOrdering
      cartDrag={cartDrag}
      onPlaced={onPlaced}
      restaurantId={restaurantId}
    />
  );
}

function KitchenDropColumn({ children }: { children: ReactNode }) {
  const drop = useDropTarget("cart", (cargo) => cargo.submit());
  return (
    <BoardColumn column="kitchen" drop={drop}>
      {children}
    </BoardColumn>
  );
}

function BoardColumn({
  column,
  drop,
  children,
}: {
  column: Column;
  drop?: ReturnType<typeof useDropTarget>;
  children: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <section
      aria-labelledby={`board-column-${column}-title`}
      className="board-column"
      data-column={column}
      {...drop}
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
