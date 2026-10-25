import { type ReactNode, useCallback, useState } from "react";
import { useQuery } from "urql";

import { BoardOrderStatusDocument } from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import {
  boardUser,
  SignInButton,
  useSignInAvailable,
} from "../../shared/auth/sign-in";
import { AsRole, useCanAct } from "../../shared/session/roles";
import { Illustration, SidePanel } from "../../shared/ui";
import { RestaurantPicker } from "../browse-restaurants";
import { OrderDetails } from "../view-order";
import { RestaurantOrdering } from "../view-restaurant";
import { BoardDragProvider, useDragSource, useDropTarget } from "./board-dnd";
import { useBoardOrders } from "./board-orders";
import { CourierColumn } from "./courier-column";
import { KitchenColumn } from "./kitchen-column";
import { OrderStrip, OrderTimeline } from "./order-flow";
import "./flow-board.css";

type Column = "customer" | "kitchen" | "courier";

const columnTitle: Record<Column, MessageKey> = {
  customer: "board.customer",
  kitchen: "board.kitchen",
  courier: "board.courier",
};

// The whole order flow on one screen: the customer orders, the kitchen cooks,
// the courier delivers, each column as its own user. Order details open in a
// side panel. The three columns stay side by side on any screen.
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
  const [selectedOrderId, setSelectedOrderId] = useState<string | null>(
    () => orders.ids[0] ?? null,
  );
  const [customerRestaurantId, setCustomerRestaurantId] = useState<
    string | null
  >(null);
  const [detailsOpen, setDetailsOpen] = useState(false);
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

      <section aria-labelledby="board-orders-title" className="board-top">
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
            onDetails={() => setDetailsOpen(true)}
            orderId={selectedOrderId}
          />
        ) : null}
      </section>

      <div className="board-columns">
        <BoardColumn column="customer">
          <RestaurantPicker
            onSelect={(restaurantId) => {
              setCustomerRestaurantId(restaurantId);
              setKitchenRestaurantId(restaurantId);
            }}
            selectedId={customerRestaurantId}
          />
          {customerRestaurantId ? (
            <RestaurantOrdering
              key={customerRestaurantId}
              onPlaced={placed}
              restaurantId={customerRestaurantId}
              ticket={(submit) => <OrderTicket submit={submit} />}
            />
          ) : (
            <div className="board-empty-state">
              <Illustration name="order-food" size="sm" />
              <p className="board-empty">{t("board.customer.pick")}</p>
            </div>
          )}
        </BoardColumn>

        <KitchenDropColumn>
          {canAct ? (
            <KitchenColumn
              onSelectOrder={setSelectedOrderId}
              preferredRestaurantId={kitchenRestaurantId}
              selectedOrderId={selectedOrderId}
            />
          ) : (
            <div className="board-empty-state">
              <Illustration name="take-out-boxes" size="sm" />
              <div className="board-empty-state">
                <Illustration name="delivery-truck" size="sm" />
                <p className="board-empty">{t("board.session.neededSignIn")}</p>
              </div>
            </div>
          )}
        </KitchenDropColumn>

        <BoardColumn column="courier">
          {canAct ? (
            <AsRole as="courier">
              <CourierColumn
                onSelectOrder={setSelectedOrderId}
                selectedOrderId={selectedOrderId}
              />
            </AsRole>
          ) : (
            <p className="board-empty">{t("board.session.neededSignIn")}</p>
          )}
        </BoardColumn>
      </div>

      <SidePanel
        closeLabel={t("board.close")}
        isOpen={detailsOpen && selectedOrderId !== null}
        onClose={() => setDetailsOpen(false)}
        title={t("board.details")}
      >
        {selectedOrderId ? <OrderDetails orderId={selectedOrderId} /> : null}
      </SidePanel>
    </div>
  );
}

// The assembled cart, ready to be carried to the kitchen; dropping it there
// sends the checkout form, the same as its button.
function OrderTicket({ submit }: { submit: () => void }) {
  const { t } = useI18n();
  const drag = useDragSource({ kind: "ticket", submit });
  return (
    <div className="board-ticket" {...drag}>
      <span aria-hidden="true" className="board-grip" />
      {t("board.dnd.ticket")}
    </div>
  );
}

// The kitchen column takes the customer's ticket: the order is placed.
function KitchenDropColumn({ children }: { children: ReactNode }) {
  const drop = useDropTarget("ticket", (cargo) => cargo.submit());
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
        <p>{t(`board.${column}.lead` as MessageKey)}</p>
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
