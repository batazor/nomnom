import { type FormEvent, useEffect, useRef, useState } from "react";
import { useMutation } from "urql";

import { boardUser, SignInButton } from "../../shared/auth/sign-in";
import { bffErrorCodes } from "../../shared/graphql/errors";
import { PlaceOrderDocument } from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { useZonedFormatters } from "../../shared/i18n/zoned-time";
import { useMarketTimeZone } from "../../shared/market/market";
import { useCanAct } from "../../shared/session/roles";
import { Button } from "../../shared/ui";
import { type DeliveryWindow, groupWindows } from "./delivery-windows";
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
      // Nearest available windows offered instead of a full one (FR-690).
      alternatives: readonly DeliveryWindow[];
    }
  // «Ко времени» without a chosen window: nothing is sent.
  | { kind: "windowRequired" }
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

// An answer later than this is no longer the usual short wait: the customer is
// told the server is still busy and that pressing again is not needed.
const slowSendingMs = 3000;

export function PlaceOrder({
  restaurantId,
  expectedWaitMinutes,
  premium,
  slots,
  lines,
  onItemProblems,
  onPlaced,
}: {
  restaurantId: string;
  // Estimate of the restaurant for an order placed now, not a promise.
  expectedWaitMinutes: number | undefined;
  // Undefined when the restaurant offers no Premium Priority.
  premium: PremiumOffer | undefined;
  // Delivery windows of scheduled orders; empty when the restaurant takes
  // none, then there is no choice of time (FR-686).
  slots: readonly DeliveryWindow[];
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
  const [slow, setSlow] = useState(false);
  const [mode, setMode] = useState<"asap" | "scheduled">("asap");
  const [windowStart, setWindowStart] = useState<string | null>(null);
  // A restaurant that lists no windows offers no choice: the order is ASAP.
  const scheduling = mode === "scheduled" && slots.length > 0;
  const deliveryWindowStart = scheduling ? windowStart : null;

  useEffect(() => {
    setSlow(false);
    if (state.kind !== "sending") return;
    const timer = setTimeout(() => setSlow(true), slowSendingMs);
    return () => clearTimeout(timer);
  }, [state.kind]);

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
    if (scheduling && deliveryWindowStart === null) {
      show({ kind: "windowRequired" });
      return;
    }
    const items = lines.map(({ menuItemId, quantity }) => ({
      menuItemId,
      quantity,
    }));
    const request = JSON.stringify({
      restaurantId,
      items,
      priority,
      deliveryWindowStart,
    });
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
        // An ASAP order names no window.
        ...(deliveryWindowStart === null ? {} : { deliveryWindowStart }),
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
    // The cart stays: a full window offers the nearest free ones instead.
    show({
      kind: "problems",
      orderProblems,
      itemProblemIds: [...itemProblems.keys()],
      alternatives: payload.problems.flatMap(
        (problem) => problem.alternativeWindows,
      ),
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
          {state.alternatives.length > 0 ? (
            <div className="checkout-alternatives">
              <p>{t("checkout.alternatives")}</p>
              <Alternatives
                onSelect={(start) => {
                  setMode("scheduled");
                  setWindowStart(start);
                }}
                selected={windowStart}
                windows={state.alternatives}
              />
            </div>
          ) : null}
        </div>
      ) : null}
      {state.kind === "windowRequired" ? (
        <p className="checkout-error" role="alert">
          {t("checkout.windowRequired")}
        </p>
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

      {slots.length > 0 ? (
        <fieldset className="checkout-when">
          <legend>{t("checkout.when")}</legend>
          <label>
            <input
              checked={!scheduling}
              name="checkout-when"
              onChange={() => setMode("asap")}
              type="radio"
            />
            <span>{t("checkout.when.asap")}</span>
          </label>
          <label>
            <input
              checked={scheduling}
              name="checkout-when"
              onChange={() => setMode("scheduled")}
              type="radio"
            />
            <span>{t("checkout.when.scheduled")}</span>
          </label>
        </fieldset>
      ) : null}
      {scheduling ? (
        <WindowPicker
          onSelect={setWindowStart}
          selected={windowStart}
          windows={slots}
        />
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
      {scheduling || expectedWaitMinutes === undefined ? null : (
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
      {/* The button is busy while the order is sent: what is going on, why
          the button waits, and when it is back are told next to it. */}
      <p
        aria-live="polite"
        className={state.kind === "sending" ? "checkout-sending" : "sr-only"}
        role="status"
      >
        {state.kind === "sending"
          ? t(slow ? "checkout.sendingSlow" : "checkout.sending")
          : ""}
      </p>
    </form>
  );
}

// Windows of the days ahead in the Market time zone (FR-686): a full window
// stays visible but cannot be chosen.
function WindowPicker({
  windows,
  selected,
  onSelect,
}: {
  windows: readonly DeliveryWindow[];
  selected: string | null;
  onSelect: (start: string) => void;
}) {
  const { t } = useI18n();
  const zoned = useZonedFormatters(useMarketTimeZone());
  if (zoned === null) return null;
  const days = groupWindows(windows, zoned);
  return (
    <fieldset className="checkout-windows">
      <legend>{t("checkout.windows")}</legend>
      {days.map((day) => (
        <fieldset className="checkout-window-day" key={day.key}>
          <legend className="checkout-window-day-label">{day.label}</legend>
          <div className="checkout-window-list">
            {day.windows.map(({ window, label }) => (
              <label
                className="checkout-window"
                data-available={window.available}
                key={window.start}
              >
                <input
                  checked={window.start === selected}
                  disabled={!window.available}
                  name="checkout-window"
                  onChange={() => onSelect(window.start)}
                  type="radio"
                  value={window.start}
                />
                <span>
                  {window.available
                    ? label
                    : `${label} · ${t("checkout.windowFull")}`}
                </span>
              </label>
            ))}
          </div>
        </fieldset>
      ))}
    </fieldset>
  );
}

// Free windows next to a full one (FR-690): choosing one keeps the cart and
// waits for the customer to send the order again.
function Alternatives({
  windows,
  selected,
  onSelect,
}: {
  windows: readonly DeliveryWindow[];
  selected: string | null;
  onSelect: (start: string) => void;
}) {
  const zoned = useZonedFormatters(useMarketTimeZone());
  if (zoned === null) return null;
  const days = groupWindows(windows, zoned);
  return (
    <div className="checkout-alternative-list">
      {days.flatMap((day) =>
        day.windows.map(({ window, label }) => (
          <Button
            aria-pressed={window.start === selected}
            key={window.start}
            onClick={() => onSelect(window.start)}
            size="sm"
            variant={window.start === selected ? "primary" : "outline"}
          >
            {`${day.label}, ${label}`}
          </Button>
        )),
      )}
    </div>
  );
}

// The checkout form, so the cart bar's button can send it from afar.
export const checkoutFormId = "checkout";
