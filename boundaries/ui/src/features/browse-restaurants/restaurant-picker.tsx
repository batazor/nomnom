import { type ReactNode, useEffect, useRef } from "react";
import { Link } from "react-router-dom";

import { useI18n } from "../../shared/i18n/i18n";
import { Button } from "../../shared/ui";
import {
  type ListedRestaurant,
  useRestaurantPages,
} from "./use-restaurant-pages";
import "./browse-restaurants.css";

export function BrowseRestaurantsPage() {
  const { t } = useI18n();
  const pages = useRestaurantPages();

  if (pages.initialFailed) {
    return (
      <CatalogMessage
        action={<Button onPress={pages.retry}>{t("restaurants.retry")}</Button>}
        detail={t("restaurants.error.detail")}
        title={t("restaurants.error.title")}
      />
    );
  }
  if (pages.initialLoading) return <CatalogSkeleton />;
  if (pages.restaurants.length === 0) {
    return (
      <CatalogMessage
        detail={t("restaurants.empty.detail")}
        title={t("restaurants.empty.title")}
      />
    );
  }

  return (
    <section aria-labelledby="catalog-title" className="catalog-page">
      <header className="catalog-header">
        <h1 id="catalog-title">{t("restaurants.title")}</h1>
        <p>{t("restaurants.lead")}</p>
      </header>

      <RestaurantList
        focusFrom={pages.loadedBefore}
        restaurants={pages.restaurants}
      />

      <div className="catalog-more">
        {pages.moreFailed ? (
          <p className="catalog-more-error" role="alert">
            {t("restaurants.moreError")}
          </p>
        ) : null}
        {pages.hasMore || pages.loadingMore || pages.moreFailed ? (
          <Button
            isPending={pages.loadingMore}
            onPress={pages.moreFailed ? pages.retry : pages.loadMore}
            variant="outline"
          >
            {pages.moreFailed ? t("restaurants.retry") : t("restaurants.more")}
          </Button>
        ) : null}
        <p aria-live="polite" className="sr-only">
          {pages.loadingMore ? t("restaurants.loadingMore") : ""}
        </p>
      </div>
    </section>
  );
}

function RestaurantList({
  restaurants,
  focusFrom,
}: {
  restaurants: ListedRestaurant[];
  focusFrom: number;
}) {
  const { t } = useI18n();
  const links = useRef(new Map<number, HTMLAnchorElement>());
  const shown = useRef(restaurants.length);

  // After "show more" the focus moves to the first restaurant of the new page,
  // so keyboard and screen-reader users continue where the list grew.
  useEffect(() => {
    if (restaurants.length > shown.current && focusFrom > 0) {
      links.current.get(focusFrom)?.focus();
    }
    shown.current = restaurants.length;
  }, [restaurants.length, focusFrom]);

  return (
    <ul className="catalog-list">
      {restaurants.map((restaurant, index) => (
        <li key={restaurant.id}>
          <Link
            className="catalog-card"
            ref={(element) => {
              if (element) links.current.set(index, element);
              else links.current.delete(index);
            }}
            to={`/restaurants/${restaurant.id}`}
          >
            <h2>{restaurant.name}</h2>
            <p>{restaurant.address}</p>
            {restaurant.acceptingOrders ? null : (
              <p className="catalog-closed">{t("restaurants.closed")}</p>
            )}
          </Link>
        </li>
      ))}
    </ul>
  );
}

function CatalogMessage({
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
      aria-labelledby="catalog-message-title"
      className="catalog-page catalog-message"
    >
      <h1 id="catalog-message-title">{title}</h1>
      <p>{detail}</p>
      {action}
    </section>
  );
}

function CatalogSkeleton() {
  const { t } = useI18n();
  return (
    <section
      aria-busy="true"
      aria-labelledby="catalog-loading"
      className="catalog-page"
    >
      <h1 className="sr-only" id="catalog-loading">
        {t("restaurants.loading")}
      </h1>
      <div aria-hidden="true" className="catalog-skeleton">
        <span className="catalog-skeleton-title" />
        <span />
        <span />
        <span />
      </div>
    </section>
  );
}
