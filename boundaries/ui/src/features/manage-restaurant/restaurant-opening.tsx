import { useState } from "react";
import { useMutation } from "urql";

import {
  CloseRestaurantDocument,
  OpenRestaurantDocument,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { Switch } from "../../shared/ui";
import { type OpeningErrorKind, openingErrorKind } from "./bff-error";
import "./manage-restaurant.css";

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

// Opening of a restaurant for new orders by its owner: one switch, labelled
// with the restaurant's name when it has one, that sends the command.
export function RestaurantOpening({
  restaurant,
}: {
  restaurant: Readonly<{ id: string; name?: string; acceptingOrders: boolean }>;
}) {
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
    <section
      aria-busy={sending || undefined}
      aria-label={t("manage.opening.title")}
      className="manage-opening"
    >
      <Switch
        checked={isOpen}
        label={restaurant.name ?? t("manage.opening.title")}
        onChange={() => void toggle()}
      />
      <span className="manage-state" data-open={isOpen ? "true" : "false"}>
        {isOpen ? t("manage.state.open") : t("manage.state.closed")}
      </span>

      {state.kind === "error" ? (
        <p className="manage-error" role="alert">
          {t(errorMessage[state.error])}
        </p>
      ) : null}

      {/* The switch shows the result; the text is for screen readers. */}
      <p aria-live="polite" className="manage-feedback sr-only">
        {state.kind === "sending"
          ? t(state.opening ? "manage.sending.open" : "manage.sending.close")
          : null}
        {state.kind === "done" ? t(doneMessage(state)) : null}
      </p>
    </section>
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
