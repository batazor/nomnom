import { type ReactNode, useCallback, useState } from "react";
import { useQuery } from "urql";

import { BoardOrderStatusDocument } from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { SignInButton, useSignInAvailable } from "../../shared/auth/sign-in";
import {
  AsRole,
  type Role,
  useCanActAs,
  useRoleSession,
} from "../../shared/session/roles";
import { SidePanel } from "../../shared/ui";
import { RestaurantPicker } from "../browse-restaurants";
import { OrderDetails } from "../view-order";
import { RestaurantOrdering } from "../view-restaurant";
import { useBoardOrders } from "./board-orders";
import { CourierColumn } from "./courier-column";
import { KitchenColumn } from "./kitchen-column";
import { OrderStrip, OrderTimeline } from "./order-flow";
import { BoardRefreshProvider, useBoardRefresh } from "./use-polling";
import "./flow-board.css";

type Column = "customer" | "kitchen" | "courier";

// Users each column acts as; each can sign in through the identity provider.
const columnRoles: Record<Column, readonly Role[]> = {
  customer: ["customer"],
  kitchen: ["owner", "kitchen"],
  courier: ["courier"],
};

const roleTitle: Record<Role, MessageKey> = {
  customer: "auth.role.customer",
  owner: "auth.role.owner",
  kitchen: "auth.role.kitchen",
  courier: "auth.role.courier",
};

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
    <BoardRefreshProvider>
      <Board />
    </BoardRefreshProvider>
  );
}

function Board() {
  const { t } = useI18n();
  const refreshBoard = useBoardRefresh();
  const session = useRoleSession();
  const signInAvailable = useSignInAvailable();
  const neededKey: MessageKey = signInAvailable
    ? "board.session.neededSignIn"
    : "board.session.needed";
  const canRunKitchen = useCanActAs("owner");
  const canDeliver = useCanActAs("courier");
  const orders = useBoardOrders();
  const [selectedOrderId, setSelectedOrderId] = useState<string | null>(
    () => orders.ids[0] ?? null,
  );
  const [customerRestaurantId, setCustomerRestaurantId] = useState<
    string | null
  >(null);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const selectedRestaurantId = useOrderRestaurant(selectedOrderId);

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
    refreshBoard();
  }

  return (
    <div className="board">
      <h1 className="sr-only">{t("board.title")}</h1>

      {session.kind === "unavailable" ? (
        <p className="board-session" role="status">
          {t(
            signInAvailable
              ? "board.session.signIn"
              : "board.session.unavailable",
          )}
        </p>
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
            onSelect={setCustomerRestaurantId}
            selectedId={customerRestaurantId}
          />
          {customerRestaurantId ? (
            <RestaurantOrdering
              key={customerRestaurantId}
              onPlaced={placed}
              restaurantId={customerRestaurantId}
            />
          ) : (
            <p className="board-empty">{t("board.customer.pick")}</p>
          )}
        </BoardColumn>

        <BoardColumn column="kitchen">
          {canRunKitchen ? (
            <KitchenColumn
              demoRestaurants={
                session.kind === "ready" ? session.session.restaurants : []
              }
              onSelectOrder={setSelectedOrderId}
              preferredRestaurantId={selectedRestaurantId}
              selectedOrderId={selectedOrderId}
            />
          ) : (
            <p className="board-empty">{t(neededKey)}</p>
          )}
        </BoardColumn>

        <BoardColumn column="courier">
          {canDeliver ? (
            <AsRole as="courier">
              <CourierColumn
                onSelectOrder={setSelectedOrderId}
                selectedOrderId={selectedOrderId}
              />
            </AsRole>
          ) : (
            <p className="board-empty">{t(neededKey)}</p>
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
        <p>{t(`board.${column}.lead` as MessageKey)}</p>
        <div className="board-column-sign-in">
          {columnRoles[column].map((role) => (
            <SignInButton
              key={role}
              role={role}
              roleLabel={t(roleTitle[role])}
            />
          ))}
        </div>
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
