import { Link } from "react-router-dom";
import { useQuery } from "urql";

import { MyRestaurantsDocument } from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import { Button } from "../../shared/ui";
import { openingErrorKind } from "./bff-error";
import "./manage-restaurant.css";

// One page of the largest size BFF serves: an owner with more restaurants is
// not expected yet, so there is no "load more".
const pageSize = 100;

export function MyRestaurantsPage() {
  const { t } = useI18n();
  const [{ data, error, fetching }, reexecute] = useQuery({
    query: MyRestaurantsDocument,
    variables: { first: pageSize },
  });

  if (!data && error && !fetching) {
    const unauthenticated = openingErrorKind(error) === "unauthenticated";
    return (
      <section
        aria-labelledby="my-restaurants-title"
        className="manage-page manage-message"
      >
        <h1 id="my-restaurants-title">{t("myRestaurants.loadError.title")}</h1>
        <p>
          {unauthenticated
            ? t("manage.error.unauthenticated")
            : t("manage.loadError.detail")}
        </p>
        <Button onPress={() => reexecute({ requestPolicy: "network-only" })}>
          {t("manage.retry")}
        </Button>
      </section>
    );
  }
  if (!data) {
    return (
      <section aria-busy="true" className="manage-page">
        <h1 className="sr-only">{t("myRestaurants.loading")}</h1>
        <div aria-hidden="true" className="manage-skeleton">
          <span className="manage-skeleton-title" />
          <span />
          <span />
        </div>
      </section>
    );
  }

  const restaurants = data.myRestaurants.edges.map((edge) => edge.node);
  return (
    <section aria-labelledby="my-restaurants-title" className="manage-page">
      <header className="manage-header">
        <p className="manage-eyebrow">{t("manage.eyebrow")}</p>
        <h1 id="my-restaurants-title">{t("myRestaurants.title")}</h1>
      </header>
      {restaurants.length === 0 ? (
        <p className="manage-hint">{t("myRestaurants.empty")}</p>
      ) : (
        <ul className="my-restaurants">
          {restaurants.map((restaurant) => (
            <li key={restaurant.id}>
              <Link
                className="manage-card my-restaurant"
                to={`/manage/restaurants/${restaurant.id}`}
              >
                <span className="my-restaurant-name">{restaurant.name}</span>
                <span className="manage-hint">{restaurant.address}</span>
                <span
                  className="manage-state"
                  data-open={restaurant.acceptingOrders ? "true" : "false"}
                >
                  <span aria-hidden="true" className="manage-state-dot" />
                  {restaurant.acceptingOrders
                    ? t("manage.state.open")
                    : t("manage.state.closed")}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
