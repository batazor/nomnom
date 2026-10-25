import { type ReactNode, useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
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
import { estimateSubtotal, useMoney } from "./money";
import "./view-restaurant.css";

type ViewedRestaurant = NonNullable<ViewRestaurantQuery["restaurant"]>;

export function ViewRestaurantPage() {
  const { restaurantId = "" } = useParams();
  // A new restaurant gets its own screen state and cart.
  return <RestaurantScreen key={restaurantId} restaurantId={restaurantId} />;
}

function RestaurantScreen({ restaurantId }: { restaurantId: string }) {
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
        action={<Link to="/">{t("restaurant.back")}</Link>}
        detail={t("restaurant.notFound.detail")}
        title={t("restaurant.notFound.title")}
      />
    );
  }
  return <RestaurantView restaurant={data.restaurant} />;
}

function RestaurantView({ restaurant }: { restaurant: ViewedRestaurant }) {
  const { t } = useI18n();
  const money = useMoney();
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
  const itemCount = entries.reduce((sum, { line }) => sum + line.quantity, 0);

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
        <Link className="restaurant-back" to="/">
          <span aria-hidden="true">←</span> {t("restaurant.back")}
        </Link>
        <h1 id="restaurant-title">{restaurant.name}</h1>
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
                onPlaced={cart.clear}
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

      {itemCount > 0 ? (
        <a className="cart-jump" href="#cart">
          <span>{t("cart.jump", { count: itemCount })}</span>
          {subtotal ? <span>{money(subtotal)}</span> : null}
        </a>
      ) : null}
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
      <h1 id="restaurant-message-title">{title}</h1>
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
      <h1 className="sr-only" id="restaurant-loading">
        {t("restaurant.loading")}
      </h1>
      <div aria-hidden="true" className="restaurant-skeleton">
        <span className="restaurant-skeleton-title" />
        <span />
        <span />
        <span />
      </div>
    </section>
  );
}
