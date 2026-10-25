import { useState } from "react";
import { useMutation } from "urql";

import {
  CloseRestaurantDocument,
  OpenRestaurantDocument,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { Button } from "../../shared/ui";
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

// Opening of a restaurant for new orders by its owner: state and the command
// that flips it.
export function RestaurantOpening({
  restaurant,
}: {
  restaurant: Readonly<{ id: string; acceptingOrders: boolean }>;
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
    <section aria-label={t("manage.opening.title")} className="manage-card">
      <div className="manage-opening">
        <p className="manage-state" data-open={isOpen ? "true" : "false"}>
          <span aria-hidden="true" className="manage-state-dot" />
          {isOpen ? t("manage.state.open") : t("manage.state.closed")}
        </p>
        <Button
          isPending={sending}
          onPress={toggle}
          size="sm"
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
