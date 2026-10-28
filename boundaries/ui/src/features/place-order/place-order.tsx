import { type FormEvent, useRef, useState } from "react";
import { useMutation } from "urql";

import { boardUser, SignInButton } from "../../shared/auth/sign-in";
import { bffErrorCodes } from "../../shared/graphql/errors";
import { PlaceOrderDocument } from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { useCanAct } from "../../shared/session/roles";
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
  | { kind: "transportError" }
  // No user signed in, or BFF refused the token.
  | { kind: "signInNeeded" };

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
  const canAct = useCanAct();
  const [, placeOrder] = useMutation(PlaceOrderDocument);
  const [state, setState] = useState<CheckoutState>({ kind: "idle" });
  const [prioritized, setPrioritized] = useState(false);
  const priority = premium && prioritized ? "PREMIUM" : "STANDARD";
  const attempt = useRef<Attempt | null>(null);
  const form = useRef<HTMLFormElement>(null);

  // The form may be sent from afar (the cart bar, a drop on the kitchen):
  // what keeps the order from the kitchen is brought into view.
  function show(next: CheckoutState) {
    setState(next);
    form.current?.scrollIntoView({ behavior: "smooth", block: "center" });
  }

  async function submit(event?: FormEvent) {
    event?.preventDefault();
    if (state.kind === "sending") return;
    if (!canAct) {
      show({ kind: "signInNeeded" });
      return;
    }
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
    if (
      result.error &&
      bffErrorCodes(result.error).includes("Unauthenticated")
    ) {
      show({ kind: "signInNeeded" });
      return;
    }
    if (result.error || !payload) {
      // No automatic retry: the customer sends the same attempt again by hand.
      show({ kind: "transportError" });
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
    show({
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
      ref={form}
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
      {state.kind === "signInNeeded" ? (
        <p className="checkout-error" role="alert">
          {t("checkout.signInNeeded")}
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
      {canAct ? (
        <Button fullWidth isPending={state.kind === "sending"} type="submit">
          {state.kind === "transportError"
            ? t("checkout.retry")
            : t("checkout.submit")}
        </Button>
      ) : (
        <div className="checkout-sign-in">
          {state.kind === "signInNeeded" ? null : (
            <p>{t("checkout.signInNeeded")}</p>
          )}
          <SignInButton role={boardUser} roleLabel={t("auth.role.demo")} />
        </div>
      )}
      <p aria-live="polite" className="sr-only">
        {state.kind === "sending" ? t("checkout.sending") : ""}
      </p>
    </form>
  );
}

// The checkout form, so the cart bar's button can send it from afar.
export const checkoutFormId = "checkout";
