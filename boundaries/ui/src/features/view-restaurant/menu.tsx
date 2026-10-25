import { useI18n } from "../../shared/i18n/i18n";
import { Button } from "../../shared/ui";
import { maxQuantity } from "./cart";
import { type Money, useMoney } from "./money";

export type MenuEntry = Readonly<{
  id: string;
  name: string;
  description?: string | null;
  price: Money;
  available: boolean;
}>;

export function Menu({
  items,
  quantities,
  acceptingOrders,
  onAdd,
}: {
  items: readonly MenuEntry[];
  quantities: ReadonlyMap<string, number>;
  acceptingOrders: boolean;
  onAdd: (item: MenuEntry) => void;
}) {
  const { t } = useI18n();
  const money = useMoney();

  return (
    <section aria-labelledby="menu-title" className="restaurant-menu">
      <h4 id="menu-title">{t("menu.title")}</h4>
      <ul className="menu-items">
        {items.map((item) => {
          const inCart = quantities.get(item.id) ?? 0;
          const soldOutId = `menu-sold-out-${item.id}`;
          return (
            <li data-available={item.available} key={item.id}>
              <div className="menu-item-copy">
                <h5>{item.name}</h5>
                {item.description ? <p>{item.description}</p> : null}
                <p className="menu-item-price">{money(item.price)}</p>
              </div>
              <div className="menu-item-action">
                {/* An unavailable item stays visible, but cannot be added. */}
                <Button
                  aria-describedby={item.available ? undefined : soldOutId}
                  aria-label={t("menu.addLabel", { item: item.name })}
                  disabled={
                    !item.available || !acceptingOrders || inCart >= maxQuantity
                  }
                  onClick={() => onAdd(item)}
                  size="sm"
                  variant="outline"
                >
                  {t("menu.add")}
                </Button>
                {item.available ? null : (
                  <span className="menu-item-sold-out" id={soldOutId}>
                    {t("menu.unavailable")}
                  </span>
                )}
                {inCart > 0 ? (
                  <span className="menu-item-in-cart">
                    {t("menu.inCart", { count: inCart })}
                  </span>
                ) : null}
              </div>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
