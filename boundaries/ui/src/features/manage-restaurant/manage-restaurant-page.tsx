import { type ReactNode, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery } from "urql";

import {
  CloseRestaurantDocument,
  ManageRestaurantDocument,
  type ManageRestaurantQuery,
  OpenRestaurantDocument,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { Button } from "../../shared/ui";
import { type OpeningErrorKind, openingErrorKind } from "./bff-error";
import "./manage-restaurant.css";

type ManagedRestaurant = NonNullable<ManageRestaurantQuery["restaurant"]>;

type OpeningState =
  | { kind: "idle" }
  | { kind: "sending"; opening: boolean }
  | { kind: "done"; opening: boolean; changed: boolean }
  | { kind: "error"; error: OpeningErrorKind };

const errorMessage: Record<OpeningErrorKind, MessageKey> = {
  notFound: "manage.error.notFound",
  notPermitted: "manage.error.notPermitted",
  unauthenticated: "manage.error.unauthenticated",
  failed: "manage.error.failed",
};

export function ManageRestaurantPage() {
  const { restaurantId = "" } = useParams();
  return <ManageScreen key={restaurantId} restaurantId={restaurantId} />;
}

function ManageScreen({ restaurantId }: { restaurantId: string }) {
  const { t } = useI18n();
  const [{ data, error, fetching }, reexecute] = useQuery({
    query: ManageRestaurantDocument,
    variables: { id: restaurantId },
  });

  if (!data && error && !fetching) {
    return (
      <ManageMessage
        action={
          <Button onPress={() => reexecute({ requestPolicy: "network-only" })}>
            {t("manage.retry")}
          </Button>
        }
        detail={t("manage.loadError.detail")}
        title={t("manage.loadError.title")}
      />
    );
  }
  if (!data) return <ManageSkeleton />;
  if (!data.restaurant) {
    return (
      <ManageMessage
        detail={t("restaurant.notFound.detail")}
        title={t("restaurant.notFound.title")}
      />
    );
  }
  return <RestaurantOpening restaurant={data.restaurant} />;
}

function RestaurantOpening({ restaurant }: { restaurant: ManagedRestaurant }) {
  const { t } = useI18n();
  const [, openRestaurant] = useMutation(OpenRestaurantDocument);
  const [, closeRestaurant] = useMutation(CloseRestaurantDocument);
  const [state, setState] = useState<OpeningState>({ kind: "idle" });
  // The opening state confirmed by the last command; the query catches up
  // when urql refetches it after the mutation.
  const [confirmed, setConfirmed] = useState<boolean | null>(null);
  const isOpen = confirmed ?? restaurant.acceptingOrders;
  const sending = state.kind === "sending";

  // Opening is idempotent in restaurants, so pressing again after a failure
  // is a safe manual retry; there is no automatic retry.
  async function send(opening: boolean) {
    const variables = { restaurantId: restaurant.id };
    if (opening) {
      const result = await openRestaurant(variables);
      return { error: result.error, payload: result.data?.openRestaurant };
    }
    const result = await closeRestaurant(variables);
    return { error: result.error, payload: result.data?.closeRestaurant };
  }

  async function toggle() {
    if (sending) return;
    const opening = !isOpen;
    setState({ kind: "sending", opening });
    const { error, payload } = await send(opening);
    if (error || !payload) {
      setState({
        kind: "error",
        error: error ? openingErrorKind(error) : "failed",
      });
      return;
    }
    setConfirmed(payload.restaurant.acceptingOrders);
    setState({ kind: "done", opening, changed: payload.changed });
  }

  return (
    <article aria-labelledby="manage-title" className="manage-page">
      <header className="manage-header">
        <Link className="manage-back" to={`/restaurants/${restaurant.id}`}>
          <span aria-hidden="true">←</span> {t("manage.viewAsCustomer")}
        </Link>
        <p className="manage-eyebrow">{t("manage.eyebrow")}</p>
        <h1 id="manage-title">{restaurant.name}</h1>
        <p>{restaurant.address}</p>
      </header>

      <section aria-labelledby="manage-opening-title" className="manage-card">
        <div className="manage-opening">
          <div>
            <h2 id="manage-opening-title">{t("manage.opening.title")}</h2>
            <p className="manage-state" data-open={isOpen ? "true" : "false"}>
              <span aria-hidden="true" className="manage-state-dot" />
              {isOpen ? t("manage.state.open") : t("manage.state.closed")}
            </p>
            <p className="manage-hint">
              {isOpen ? t("manage.hint.open") : t("manage.hint.closed")}
            </p>
          </div>
          <Button
            isPending={sending}
            onPress={toggle}
            variant={isOpen ? "outline" : "primary"}
          >
            {isOpen ? t("manage.close") : t("manage.open")}
          </Button>
        </div>

        {state.kind === "error" ? (
          <p className="manage-error" role="alert">
            {t(errorMessage[state.error])}
          </p>
        ) : null}

        <p aria-live="polite" className="manage-feedback">
          {state.kind === "sending"
            ? t(state.opening ? "manage.sending.open" : "manage.sending.close")
            : null}
          {state.kind === "done" ? t(doneMessage(state)) : null}
        </p>
      </section>
    </article>
  );
}

function doneMessage({
  opening,
  changed,
}: {
  opening: boolean;
  changed: boolean;
}): MessageKey {
  if (opening)
    return changed ? "manage.done.opened" : "manage.done.alreadyOpen";
  return changed ? "manage.done.closed" : "manage.done.alreadyClosed";
}

function ManageMessage({
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
      aria-labelledby="manage-message-title"
      className="manage-page manage-message"
    >
      <h1 id="manage-message-title">{title}</h1>
      <p>{detail}</p>
      {action}
    </section>
  );
}

function ManageSkeleton() {
  const { t } = useI18n();
  return (
    <section
      aria-busy="true"
      aria-labelledby="manage-loading"
      className="manage-page"
    >
      <h1 className="sr-only" id="manage-loading">
        {t("manage.loading")}
      </h1>
      <div aria-hidden="true" className="manage-skeleton">
        <span className="manage-skeleton-title" />
        <span />
        <span />
      </div>
    </section>
  );
}
