import { type ReactNode, useEffect, useMemo, useState } from "react";
import { useQuery } from "urql";

import {
  ViewRestaurantDocument,
  type ViewRestaurantQuery,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import { Button } from "../../shared/ui";
import { type ItemProblems, PlaceOrder } from "../place-order";
import { useCart } from "./cart";
import { type CartEntry, CartPanel } from "./cart-panel";
import { Menu, type MenuEntry } from "./menu";
import { estimateSubtotal } from "./money";
import "./view-restaurant.css";

type ViewedRestaurant = NonNullable<ViewRestaurantQuery["restaurant"]>;

// Menu, cart and checkout of one restaurant. The caller keys it by
// restaurantId, so another restaurant gets its own state and cart.
export function RestaurantOrdering({
  restaurantId,
  onPlaced,
}: {
  restaurantId: string;
  onPlaced: (orderId: string) => void;
}) {
  const { t } = useI18n();
  const [{ data, error, fetching }, reexecute] = useQuery({
    query: ViewRestaurantDocument,
    variables: { id: restaurantId },
  });

  if (!data && error && !fetching) {
    return (
      <RestaurantMessage
        action={
          <Button onPress={() => reexecute({ requestPolicy: "network-only" })}>
            {t("restaurant.retry")}
          </Button>
        }
        detail={t("restaurant.error.detail")}
        title={t("restaurant.error.title")}
      />
    );
  }
  if (!data) return <RestaurantSkeleton />;
  if (!data.restaurant) {
    return (
      <RestaurantMessage
        detail={t("restaurant.notFound.detail")}
        title={t("restaurant.notFound.title")}
      />
    );
  }
  return <RestaurantView onPlaced={onPlaced} restaurant={data.restaurant} />;
}

function RestaurantView({
  restaurant,
  onPlaced,
}: {
  restaurant: ViewedRestaurant;
  onPlaced: (orderId: string) => void;
}) {
  const { t } = useI18n();
  const cart = useCart(restaurant.id);
  const [itemProblems, setItemProblems] = useState<ItemProblems>(
    () => new Map(),
  );
  const [announcement, setAnnouncement] = useState("");
  const menu = restaurant.menu;

  const menuById = useMemo(
    () => new Map((menu?.items ?? []).map((item) => [item.id, item])),
    [menu],
  );

  // Lines of items that left the menu cannot be ordered or priced.
  const { retain } = cart;
  useEffect(() => {
    if (menu) retain(new Set(menuById.keys()));
  }, [menu, menuById, retain]);

  const entries: CartEntry[] = cart.lines.flatMap((line) => {
    const item = menuById.get(line.menuItemId);
    return item ? [{ line, item }] : [];
  });
  const quantities = new Map(
    entries.map(({ line }) => [line.menuItemId, line.quantity]),
  );
  const subtotal = estimateSubtotal(
    entries.map(({ line, item }) => ({
      price: item.price,
      quantity: line.quantity,
    })),
  );

  function add(item: MenuEntry) {
    cart.add(item.id);
    setAnnouncement(
      t("menu.added", {
        item: item.name,
        count: (quantities.get(item.id) ?? 0) + 1,
      }),
    );
  }

  return (
    <article aria-labelledby="restaurant-title" className="restaurant-page">
      <header className="restaurant-header">
        <h3 id="restaurant-title">{restaurant.name}</h3>
        <p>{restaurant.address}</p>
        {restaurant.acceptingOrders ? null : (
          <p className="restaurant-closed" role="status">
            {t("restaurant.closed")}
          </p>
        )}
      </header>

      {menu ? (
        <div className="restaurant-layout">
          <Menu
            acceptingOrders={restaurant.acceptingOrders}
            items={menu.items}
            onAdd={add}
            quantities={quantities}
          />
          <CartPanel
            entries={entries}
            itemProblems={itemProblems}
            onQuantity={cart.setQuantity}
            onRemove={cart.remove}
            subtotal={subtotal}
          >
            {restaurant.acceptingOrders ? (
              <PlaceOrder
                lines={entries.map(({ line }) => line)}
                onItemProblems={setItemProblems}
                onPlaced={(orderId) => {
                  cart.clear();
                  onPlaced(orderId);
                }}
                restaurantId={restaurant.id}
              />
            ) : null}
          </CartPanel>
        </div>
      ) : (
        <p className="restaurant-no-menu">{t("restaurant.noMenu")}</p>
      )}

      <p aria-live="polite" className="sr-only">
        {announcement}
      </p>
    </article>
  );
}

function RestaurantMessage({
  title,
  detail,
  action,
}: {
  title: string;
  detail: string;
  action?: ReactNode;
}) {
  return (
    <section
      aria-labelledby="restaurant-message-title"
      className="restaurant-page restaurant-message"
    >
      <h3 id="restaurant-message-title">{title}</h3>
      <p>{detail}</p>
      {action}
    </section>
  );
}

function RestaurantSkeleton() {
  const { t } = useI18n();
  return (
    <section
      aria-busy="true"
      aria-labelledby="restaurant-loading"
      className="restaurant-page"
    >
      <h3 className="sr-only" id="restaurant-loading">
        {t("restaurant.loading")}
      </h3>
      <div aria-hidden="true" className="restaurant-skeleton">
        <span className="restaurant-skeleton-title" />
        <span />
        <span />
        <span />
      </div>
    </section>
  );
}
