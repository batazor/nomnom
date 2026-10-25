import { useRef, useState } from "react";
import { useMutation } from "urql";

import {
  type OrderCancellationProblemCode,
  RequestOrderCancellationDocument,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { Button } from "../../shared/ui";

type CancelState =
  // returnsFocus brings focus back to the action after leaving the confirmation.
  | { kind: "idle"; returnsFocus?: boolean }
  | { kind: "confirming" }
  | { kind: "sending" }
  | { kind: "requested" }
  | { kind: "problem"; code: OrderCancellationProblemCode }
  | { kind: "transportError" };

const problemMessage: Record<OrderCancellationProblemCode, MessageKey> = {
  CANCELLATION_WINDOW_NOT_OPEN:
    "order.cancel.problem.CANCELLATION_WINDOW_NOT_OPEN",
  CANCELLATION_WINDOW_EXPIRED:
    "order.cancel.problem.CANCELLATION_WINDOW_EXPIRED",
  ORDER_ALREADY_FINAL: "order.cancel.problem.ORDER_ALREADY_FINAL",
};

// Only a window that has not opened yet leaves the action available.
function keepsAction(code: OrderCancellationProblemCode): boolean {
  return code === "CANCELLATION_WINDOW_NOT_OPEN";
}

export function CancelOrder({
  orderId,
  availableUntil,
  onRequested,
}: {
  orderId: string;
  availableUntil: string | null;
  onRequested: () => void;
}) {
  const { t, formatters } = useI18n();
  const [, requestCancellation] = useMutation(RequestOrderCancellationDocument);
  const [state, setState] = useState<CancelState>({ kind: "idle" });
  // One key per cancellation attempt, reused when the user retries by hand.
  const requestId = useRef<string | null>(null);

  async function send() {
    requestId.current ??= crypto.randomUUID();
    setState({ kind: "sending" });
    const result = await requestCancellation({
      input: { orderId, requestId: requestId.current },
    });
    const payload = result.data?.requestOrderCancellation;
    if (result.error || !payload) {
      setState({ kind: "transportError" });
      return;
    }
    requestId.current = null;
    if (payload.problem) {
      setState({ kind: "problem", code: payload.problem.code });
      return;
    }
    setState({ kind: "requested" });
    onRequested();
  }

  if (state.kind === "idle" && !availableUntil) return null;

  const showsAction =
    (state.kind === "idle" ||
      (state.kind === "problem" && keepsAction(state.code))) &&
    availableUntil;

  return (
    <section aria-labelledby="order-cancel-title" className="order-cancel">
      <h4 className="sr-only" id="order-cancel-title">
        {t("order.cancel.action")}
      </h4>

      {showsAction ? (
        <div className="order-cancel-row">
          <p>
            {t("order.cancel.until", {
              time: formatters.time.format(Date.parse(availableUntil)),
            })}
          </p>
          <Button
            autoFocus={state.kind === "idle" && state.returnsFocus}
            onPress={() => setState({ kind: "confirming" })}
            variant="outline"
          >
            {t("order.cancel.action")}
          </Button>
        </div>
      ) : null}

      {state.kind === "confirming" ? (
        <fieldset className="order-cancel-confirm">
          <legend>{t("order.cancel.confirmTitle")}</legend>
          <p>{t("order.cancel.confirmDetail")}</p>
          <div className="order-cancel-actions">
            <Button onPress={send} variant="danger">
              {t("order.cancel.confirm")}
            </Button>
            {/* The safe choice takes focus when the confirmation opens. */}
            <Button
              autoFocus
              onPress={() => setState({ kind: "idle", returnsFocus: true })}
              variant="ghost"
            >
              {t("order.cancel.keep")}
            </Button>
          </div>
        </fieldset>
      ) : null}

      {state.kind === "transportError" ? (
        <div className="order-cancel-row">
          <p className="order-cancel-error">
            {t("order.cancel.transportError")}
          </p>
          <Button onPress={send} variant="outline">
            {t("order.cancel.retry")}
          </Button>
        </div>
      ) : null}

      <p aria-live="polite" className="order-cancel-feedback">
        {state.kind === "sending" ? t("order.cancel.sending") : null}
        {state.kind === "requested" ? t("order.cancel.requested") : null}
        {state.kind === "problem" ? t(problemMessage[state.code]) : null}
      </p>
    </section>
  );
}
