import { useEffect, useRef } from "react";

import { useI18n } from "../../shared/i18n/i18n";
import { Button } from "../../shared/ui";
import { useRestaurantPages } from "./use-restaurant-pages";
import "./browse-restaurants.css";

// Open restaurants page by page; choosing one hands its id to the caller.
export function RestaurantPicker({
  selectedId,
  onSelect,
}: {
  selectedId: string | null;
  onSelect: (restaurantId: string) => void;
}) {
  const { t } = useI18n();
  const pages = useRestaurantPages();
  const buttons = useRef(new Map<number, HTMLButtonElement>());
  const shown = useRef(pages.restaurants.length);

  // After "show more" the focus moves to the first restaurant of the new page,
  // so keyboard and screen-reader users continue where the list grew.
  useEffect(() => {
    if (pages.restaurants.length > shown.current && pages.loadedBefore > 0) {
      buttons.current.get(pages.loadedBefore)?.focus();
    }
    shown.current = pages.restaurants.length;
  }, [pages.restaurants.length, pages.loadedBefore]);

  if (pages.initialFailed) {
    return (
      <div className="catalog-message" role="alert">
        <p>{t("restaurants.error.title")}</p>
        <Button onClick={pages.retry} size="sm">
          {t("restaurants.retry")}
        </Button>
      </div>
    );
  }
  if (pages.initialLoading) {
    return (
      <p aria-busy="true" className="catalog-message">
        {t("restaurants.loading")}
      </p>
    );
  }
  if (pages.restaurants.length === 0) {
    return <p className="catalog-message">{t("restaurants.empty.title")}</p>;
  }

  return (
    <div className="catalog-picker">
      <ul aria-label={t("restaurants.title")} className="catalog-list">
        {pages.restaurants.map((restaurant, index) => (
          <li key={restaurant.id}>
            <button
              aria-pressed={restaurant.id === selectedId}
              className="catalog-card"
              onClick={() => onSelect(restaurant.id)}
              ref={(element) => {
                if (element) buttons.current.set(index, element);
                else buttons.current.delete(index);
              }}
              type="button"
            >
              <span className="catalog-name">{restaurant.name}</span>
              <span className="catalog-address">{restaurant.address}</span>
            </button>
          </li>
        ))}
      </ul>

      {pages.moreFailed ? (
        <p className="catalog-more-error" role="alert">
          {t("restaurants.moreError")}
        </p>
      ) : null}
      {pages.hasMore || pages.loadingMore || pages.moreFailed ? (
        <Button
          isPending={pages.loadingMore}
          onClick={pages.moreFailed ? pages.retry : pages.loadMore}
          size="sm"
          variant="ghost"
        >
          {pages.moreFailed ? t("restaurants.retry") : t("restaurants.more")}
        </Button>
      ) : null}
      <p aria-live="polite" className="sr-only">
        {pages.loadingMore ? t("restaurants.loadingMore") : ""}
      </p>
    </div>
  );
}
