import { AnimatePresence, m } from "motion/react";
import { type ReactNode, useRef } from "react";

import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { AnimatedNumber, Button } from "../../shared/ui";
import { type CartLine, maxQuantity } from "./cart";
import type { MenuEntry } from "./menu";
import { estimateSubtotal, type Money, useMoney } from "./money";

export type CartEntry = Readonly<{ line: CartLine; item: MenuEntry }>;

export function CartPanel({
  entries,
  subtotal,
  itemProblems,
  onQuantity,
  onRemove,
  children,
}: {
  entries: readonly CartEntry[];
  subtotal: Money | null;
  itemProblems: ReadonlyMap<string, MessageKey>;
  onQuantity: (menuItemId: string, quantity: number) => void;
  onRemove: (menuItemId: string) => void;
  // Checkout below the cart lines, when ordering is possible.
  children?: ReactNode;
}) {
  const { t, formatters } = useI18n();
  const money = useMoney();
  const heading = useRef<HTMLHeadingElement>(null);

  function remove(menuItemId: string) {
    onRemove(menuItemId);
    // The removed line took the focus with it; continue from the cart heading.
    heading.current?.focus();
  }

  return (
    <section aria-labelledby="cart-title" className="restaurant-cart" id="cart">
      <h4 id="cart-title" ref={heading} tabIndex={-1}>
        {t("cart.title")}
      </h4>

      {entries.length === 0 ? (
        <p className="cart-empty">{t("cart.empty")}</p>
      ) : (
        <>
          <ul className="cart-lines">
            {/* A removed line fades in place, then the lines below slide up
                (layout). popLayout is avoided: it injects a <style> that the
                CSP blocks. */}
            <AnimatePresence initial={false}>
              {entries.map(({ line, item }) => {
                const problem = itemProblems.get(item.id);
                const noteId = `cart-note-${item.id}`;
                const lineTotal = estimateSubtotal([
                  { price: item.price, quantity: line.quantity },
                ]);
                const note = problem
                  ? t(problem)
                  : item.available
                    ? null
                    : t("cart.unavailable");
                return (
                  <m.li
                    animate={{ opacity: 1, y: 0 }}
                    data-problem={Boolean(note)}
                    exit={{ opacity: 0, x: -16 }}
                    initial={{ opacity: 0, y: -8 }}
                    key={item.id}
                    layout
                    transition={{ duration: 0.18, ease: "easeOut" }}
                  >
                    <div className="cart-line-head">
                      <span className="cart-line-name">{item.name}</span>
                      <span className="cart-line-total">
                        {lineTotal ? (
                          <AnimatedNumber
                            text={money(lineTotal)}
                            value={Number(lineTotal.amount)}
                          />
                        ) : null}
                      </span>
                    </div>
                    {note ? (
                      <p className="cart-line-note" id={noteId}>
                        {note}
                      </p>
                    ) : null}
                    <div className="cart-line-controls">
                      <fieldset
                        aria-describedby={note ? noteId : undefined}
                        className="cart-stepper"
                      >
                        <legend className="sr-only">{item.name}</legend>
                        <Button
                          aria-label={t("cart.decrease", { item: item.name })}
                          isIconOnly
                          // Below one the line leaves the cart, as with "Remove".
                          onClick={() =>
                            line.quantity <= 1
                              ? remove(item.id)
                              : onQuantity(item.id, line.quantity - 1)
                          }
                          size="sm"
                          variant="outline"
                        >
                          <span aria-hidden="true">−</span>
                        </Button>
                        <output
                          aria-label={t("cart.quantity", { item: item.name })}
                          aria-live="polite"
                          className="cart-quantity"
                        >
                          <AnimatedNumber
                            text={formatters.number.format(line.quantity)}
                            value={line.quantity}
                          />
                        </output>
                        <Button
                          aria-label={t("cart.increase", { item: item.name })}
                          disabled={
                            !item.available || line.quantity >= maxQuantity
                          }
                          isIconOnly
                          onClick={() => onQuantity(item.id, line.quantity + 1)}
                          size="sm"
                          variant="outline"
                        >
                          <span aria-hidden="true">+</span>
                        </Button>
                      </fieldset>
                      <Button
                        aria-label={t("cart.removeLabel", { item: item.name })}
                        onClick={() => remove(item.id)}
                        size="sm"
                        variant="ghost"
                      >
                        {t("cart.remove")}
                      </Button>
                    </div>
                  </m.li>
                );
              })}
            </AnimatePresence>
          </ul>

          {subtotal ? (
            <div className="cart-subtotal">
              <dl>
                <div>
                  <dt>{t("cart.subtotal")}</dt>
                  <dd aria-live="polite" data-testid="cart-subtotal">
                    <AnimatedNumber
                      text={money(subtotal)}
                      value={Number(subtotal.amount)}
                    />
                  </dd>
                </div>
              </dl>
              <p>{t("cart.subtotalNote")}</p>
            </div>
          ) : null}

          {children}
        </>
      )}
    </section>
  );
}
