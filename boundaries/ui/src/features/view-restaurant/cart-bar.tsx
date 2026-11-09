import { useEffect, useState } from "react";

import { useI18n } from "../../shared/i18n/i18n";
import { Button } from "../../shared/ui";
import { type Money, useMoney } from "./money";

// The cart in short, stuck to the bottom of the menu while the full cart is
// out of view: the total and the order button, so ordering needs no scroll
// down. The total opens the full cart; the button sends the checkout form.
export function CartBar({
  count,
  subtotal,
  formId,
  cartId,
}: {
  count: number;
  subtotal: Money;
  formId: string;
  cartId: string;
}) {
  const { t, formatters } = useI18n();
  const money = useMoney();
  // Hidden once the checkout form itself is on screen, not when the cart's
  // top edge peeks in: the full cart is in reach only with its order button.
  const checkoutInView = useInView(formId);
  // A bar with focus inside stays until focus has moved to the form's own
  // order button: removing it would drop focus to the body (FR-780).
  const [focusWithin, setFocusWithin] = useState(false);
  const handOver = checkoutInView && focusWithin;
  useEffect(() => {
    if (!handOver) return;
    document
      .querySelector<HTMLElement>(`#${CSS.escape(formId)} [type="submit"]`)
      ?.focus();
  }, [handOver, formId]);

  if (checkoutInView && !focusWithin) return null;
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: focus tracking only, so the bar outlives the focus inside it; its buttons are the controls.
    <div
      className="cart-bar"
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) {
          setFocusWithin(false);
        }
      }}
      onFocus={() => setFocusWithin(true)}
    >
      <button
        className="cart-bar-summary"
        onClick={() => {
          const cart = document.getElementById(cartId);
          cart?.scrollIntoView({ behavior: "smooth", block: "start" });
          // Focus follows the eye to the full cart's heading.
          cart?.querySelector<HTMLElement>("h4")?.focus({ preventScroll: true });
        }}
        type="button"
      >
        <span className="cart-bar-title">{t("cart.title")}</span>
        <span className="cart-bar-total">
          {t("cart.summary", {
            count: formatters.number.format(count),
            total: money(subtotal),
          })}
        </span>
      </button>
      <Button form={formId} size="sm" type="submit">
        {t("cart.checkout")}
      </Button>
    </div>
  );
}

// Whether most of the element with this id shows on screen; true until
// known, so the bar never doubles a form that is already there.
const mostlyVisible = 0.75;

function useInView(id: string): boolean {
  const [inView, setInView] = useState(true);
  useEffect(() => {
    const element = document.getElementById(id);
    if (!element) return;
    const observer = new IntersectionObserver(
      ([entry]) => setInView(entry.intersectionRatio >= mostlyVisible),
      { threshold: [0, mostlyVisible] },
    );
    observer.observe(element);
    return () => observer.disconnect();
  }, [id]);
  return inView;
}
