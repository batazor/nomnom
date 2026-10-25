import { type FormEvent, useRef, useState } from "react";
import { useMutation } from "urql";

import { PlaceOrderDocument } from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { Button, TextField } from "../../shared/ui";
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
// cart or the address starts a new attempt.
type Attempt = { key: string; request: string };

export function PlaceOrder({
  restaurantId,
  lines,
  onItemProblems,
  onPlaced,
}: {
  restaurantId: string;
  lines: readonly OrderLine[];
  onItemProblems: (problems: ItemProblems) => void;
  onPlaced: (orderId: string) => void;
}) {
  const { t } = useI18n();
  const [, placeOrder] = useMutation(PlaceOrderDocument);
  const [address, setAddress] = useState("");
  const [addressMissing, setAddressMissing] = useState(false);
  const [state, setState] = useState<CheckoutState>({ kind: "idle" });
  const attempt = useRef<Attempt | null>(null);
  const addressInput = useRef<HTMLInputElement>(null);

  async function submit(event?: FormEvent) {
    event?.preventDefault();
    if (state.kind === "sending") return;
    const deliveryAddress = address.trim();
    if (!deliveryAddress) {
      setAddressMissing(true);
      addressInput.current?.focus();
      return;
    }

    const items = lines.map(({ menuItemId, quantity }) => ({
      menuItemId,
      quantity,
    }));
    const request = JSON.stringify({ restaurantId, items, deliveryAddress });
    if (attempt.current?.request !== request) {
      attempt.current = { key: crypto.randomUUID(), request };
    }

    setState({ kind: "sending" });
    const result = await placeOrder({
      input: {
        restaurantId,
        items,
        deliveryAddress,
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

      <TextField
        autoComplete="street-address"
        description={t("checkout.addressHint")}
        errorMessage={addressMissing ? t("checkout.addressRequired") : null}
        inputRef={addressInput}
        required
        label={t("checkout.address")}
        name="deliveryAddress"
        onChange={(value) => {
          setAddress(value);
          if (value.trim()) setAddressMissing(false);
        }}
        value={address}
      />

      {state.kind === "transportError" ? (
        <p className="checkout-error" role="alert">
          {t("checkout.transportError")}
        </p>
      ) : null}

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
