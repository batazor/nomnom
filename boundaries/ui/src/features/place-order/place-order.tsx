import { type FormEvent, useRef, useState } from "react";
import { useMutation } from "urql";

import { PlaceOrderDocument } from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { Button } from "../../shared/ui";
import { type ItemProblems, splitProblems } from "./problems";
import "./place-order.css";

export type OrderLine = Readonly<{ menuItemId: string; quantity: number }>;

type CheckoutState =
  | { kind: "idle" }
  | { kind: "sending" }
  | {
      kind: "problems";
      orderProblems: MessageKey[];
      itemProblemIds: string[];
    }
  | { kind: "transportError" };

// One checkout attempt: its idempotency key and the exact request it was made
// for. A manual retry of the same request reuses the key; any change to the
// cart starts a new attempt.
type Attempt = { key: string; request: string };

// Premium Priority offered by the restaurant (FR-653): its fee and the cart
// total with it, both formatted by the caller; no estimate of time.
export type PremiumOffer = Readonly<{ fee: string; total: string | null }>;

export function PlaceOrder({
  restaurantId,
  expectedWaitMinutes,
  premium,
  lines,
  onItemProblems,
  onPlaced,
}: {
  restaurantId: string;
  // Estimate of the restaurant for an order placed now, not a promise.
  expectedWaitMinutes: number | undefined;
  // Undefined when the restaurant offers no Premium Priority.
  premium: PremiumOffer | undefined;
  lines: readonly OrderLine[];
  onItemProblems: (problems: ItemProblems) => void;
  onPlaced: (orderId: string) => void;
}) {
  const { t } = useI18n();
  const [, placeOrder] = useMutation(PlaceOrderDocument);
  const [state, setState] = useState<CheckoutState>({ kind: "idle" });
  const [prioritized, setPrioritized] = useState(false);
  const priority = premium && prioritized ? "PREMIUM" : "STANDARD";
  const attempt = useRef<Attempt | null>(null);

  async function submit(event?: FormEvent) {
    event?.preventDefault();
    if (state.kind === "sending") return;
    const items = lines.map(({ menuItemId, quantity }) => ({
      menuItemId,
      quantity,
    }));
    const request = JSON.stringify({ restaurantId, items, priority });
    if (attempt.current?.request !== request) {
      attempt.current = { key: crypto.randomUUID(), request };
    }

    setState({ kind: "sending" });
    const result = await placeOrder({
      input: {
        restaurantId,
        items,
        priority,
        idempotencyKey: attempt.current.key,
      },
    });
    const payload = result.data?.placeOrder;
    if (result.error || !payload) {
      // No automatic retry: the customer sends the same attempt again by hand.
      setState({ kind: "transportError" });
      return;
    }

    attempt.current = null;
    if (payload.order) {
      onItemProblems(new Map());
      onPlaced(payload.order.id);
      return;
    }
    const { orderProblems, itemProblems } = splitProblems(payload.problems);
    onItemProblems(itemProblems);
    setState({
      kind: "problems",
      orderProblems,
      itemProblemIds: [...itemProblems.keys()],
    });
  }

  const inCart = new Set(lines.map((line) => line.menuItemId));
  const itemProblemsInCart =
    state.kind === "problems" &&
    state.itemProblemIds.some((id) => inCart.has(id));

  return (
    <form
      aria-label={t("checkout.title")}
      className="checkout"
      id={checkoutFormId}
      noValidate
      onSubmit={submit}
    >
      {state.kind === "problems" &&
      (state.orderProblems.length > 0 || itemProblemsInCart) ? (
        <div className="checkout-problems" role="alert">
          {state.orderProblems.map((key) => (
            <p key={key}>{t(key)}</p>
          ))}
          {itemProblemsInCart ? <p>{t("checkout.itemProblems")}</p> : null}
        </div>
      ) : null}

      {state.kind === "transportError" ? (
        <p className="checkout-error" role="alert">
          {t("checkout.transportError")}
        </p>
      ) : null}

      {premium ? (
        <label className="checkout-premium">
          <input
            checked={prioritized}
            onChange={(event) => setPrioritized(event.target.checked)}
            type="checkbox"
          />
          <span>{t("checkout.premium", { fee: premium.fee })}</span>
        </label>
      ) : null}
      {premium && prioritized && premium.total ? (
        <p className="checkout-premium-total">
          {t("checkout.totalWithPremium", { total: premium.total })}
        </p>
      ) : null}
      {expectedWaitMinutes === undefined ? null : (
        <p className="checkout-wait">
          {t("checkout.expectedWait", { minutes: expectedWaitMinutes })}
        </p>
      )}
      <Button fullWidth isPending={state.kind === "sending"} type="submit">
        {state.kind === "transportError"
          ? t("checkout.retry")
          : t("checkout.submit")}
      </Button>
      <p aria-live="polite" className="sr-only">
        {state.kind === "sending" ? t("checkout.sending") : ""}
      </p>
    </form>
  );
}

// The checkout form, so the cart bar's button can send it from afar.
export const checkoutFormId = "checkout";
