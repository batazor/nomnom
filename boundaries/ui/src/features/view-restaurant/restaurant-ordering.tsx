import { m } from "motion/react";
import {
  type HTMLAttributes,
  type ReactNode,
  useEffect,
  useMemo,
  useState,
} from "react";
import { useQuery } from "urql";

import {
  ViewRestaurantDocument,
  type ViewRestaurantQuery,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import { Button, useToast } from "../../shared/ui";
import { checkoutFormId, type ItemProblems, PlaceOrder } from "../place-order";
import { useCart } from "./cart";
import { CartBar } from "./cart-bar";
import { type CartEntry, CartPanel } from "./cart-panel";
import { Menu, type MenuEntry } from "./menu";
import { estimateSubtotal, useMoney } from "./money";
import "./view-restaurant.css";

type ViewedRestaurant = NonNullable<ViewRestaurantQuery["restaurant"]>;

// Menu, cart and checkout of one restaurant. The caller keys it by
// restaurantId, so another restaurant gets its own state and cart.
export function RestaurantOrdering({
  restaurantId,
  onPlaced,
  cartDrag,
}: {
  restaurantId: string;
  onPlaced: (orderId: string) => void;
  // Props that let the filled cart be carried somewhere else, e.g. dragged
  // to the kitchen on the board.
  cartDrag?: HTMLAttributes<HTMLElement>;
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
          <Button onClick={() => reexecute({ requestPolicy: "network-only" })}>
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
  return (
    <RestaurantView
      cartDrag={cartDrag}
      onPlaced={onPlaced}
      restaurant={data.restaurant}
    />
  );
}

function RestaurantView({
  restaurant,
  onPlaced,
  cartDrag,
}: {
  restaurant: ViewedRestaurant;
  onPlaced: (orderId: string) => void;
  cartDrag?: HTMLAttributes<HTMLElement>;
}) {
  const { t } = useI18n();
  const cart = useCart(restaurant.id);
  const [itemProblems, setItemProblems] = useState<ItemProblems>(
    () => new Map(),
  );
  const toast = useToast();
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
  const priced = entries.map(({ line, item }) => ({
    price: item.price,
    quantity: line.quantity,
  }));
  const subtotal = estimateSubtotal(priced);
  const money = useMoney();
  // FR-653: the cart total with the premium fee, an estimate like the
  // subtotal; placeOrder computes the real total.
  const fee = restaurant.premiumFee;
  const premium = fee
    ? {
        fee: money(fee),
        total: (() => {
          const total = subtotal
            ? estimateSubtotal([...priced, { price: fee, quantity: 1 }])
            : null;
          return total ? money(total) : null;
        })(),
      }
    : undefined;

  function add(item: MenuEntry) {
    cart.add(item.id);
    toast({
      title: item.name,
      description: t("menu.added", {
        count: (quantities.get(item.id) ?? 0) + 1,
      }),
    });
  }

  return (
    <article aria-labelledby="restaurant-title" className="restaurant-page">
      {/* The restaurant card of the list grows into this header. */}
      <m.header
        className="restaurant-header"
        layoutId={`restaurant-${restaurant.id}`}
        transition={{ type: "spring", bounce: 0.15, duration: 0.4 }}
      >
        <m.h3 id="restaurant-title" layout="position">
          {restaurant.name}
        </m.h3>
        {restaurant.acceptingOrders ? null : (
          <p className="restaurant-closed" role="status">
            {t("restaurant.closed")}
          </p>
        )}
        {restaurant.waitTime?.elevated ? (
          <p className="restaurant-wait">{t("restaurants.waitElevated")}</p>
        ) : null}
      </m.header>

      {menu ? (
        <div className="restaurant-layout">
          <div className="restaurant-menu-column">
            <Menu
              acceptingOrders={restaurant.acceptingOrders}
              items={menu.items}
              onAdd={add}
              quantities={quantities}
              restaurantId={restaurant.id}
            />
            {restaurant.acceptingOrders && subtotal ? (
              <CartBar
                cartId="cart"
                count={entries.reduce(
                  (sum, { line }) => sum + line.quantity,
                  0,
                )}
                formId={checkoutFormId}
                subtotal={subtotal}
              />
            ) : null}
          </div>
          <div
            className="restaurant-cart-carrier"
            {...(restaurant.acceptingOrders && entries.length > 0
              ? cartDrag
              : {})}
          >
            <CartPanel
              entries={entries}
              itemProblems={itemProblems}
              onQuantity={cart.setQuantity}
              onRemove={cart.remove}
              subtotal={subtotal}
            >
              {restaurant.acceptingOrders ? (
                <PlaceOrder
                  expectedWaitMinutes={restaurant.waitTime?.expectedWaitMinutes}
                  premium={premium}
                  lines={entries.map(({ line }) => line)}
                  onItemProblems={setItemProblems}
                  onPlaced={(orderId) => {
                    cart.clear();
                    toast({
                      title: t("order.sentToKitchen"),
                      description: restaurant.name,
                    });
                    onPlaced(orderId);
                  }}
                  restaurantId={restaurant.id}
                />
              ) : null}
            </CartPanel>
          </div>
        </div>
      ) : (
        <p className="restaurant-no-menu">{t("restaurant.noMenu")}</p>
      )}
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
