import { useReducer, useState } from "react";

import { useI18n } from "../../shared/i18n/i18n";
import { Button } from "../../shared/ui";
import { customerJourneyFixture, initialJourneyState } from "./fixtures";
import { deriveSubtotalMinor, journeyReducer } from "./model";

export function CustomerJourneyPreview() {
  const { t, formatters } = useI18n();
  const [state, dispatch] = useReducer(journeyReducer, initialJourneyState);
  const [reviewed, setReviewed] = useState(false);
  const currency = formatters.currency("RUB");
  const subtotalMinor = deriveSubtotalMinor(
    customerJourneyFixture.menu,
    state.quantities,
  );
  const totalMinor =
    subtotalMinor +
    customerJourneyFixture.checkout.deliveryMinor +
    customerJourneyFixture.checkout.serviceMinor;
  const activeStageIndex = customerJourneyFixture.deliveryStages.findIndex(
    ({ id }) => id === state.deliveryStage,
  );
  const activeStage =
    customerJourneyFixture.deliveryStages[activeStageIndex] ??
    customerJourneyFixture.deliveryStages[0];

  const formatMoney = (minor: number) => currency.format(minor / 100);
  const showNextStage = () => {
    const nextIndex = Math.min(
      activeStageIndex + 1,
      customerJourneyFixture.deliveryStages.length - 1,
    );
    const nextStage = customerJourneyFixture.deliveryStages[nextIndex];
    if (nextStage) {
      dispatch({ type: "set-delivery-stage", stageId: nextStage.id });
    }
  };

  return (
    <section aria-labelledby="journey-title" className="journey-page">
      <header className="journey-intro">
        <div className="journey-title-row">
          <h1 id="journey-title">{t("journey.title")}</h1>
          <span className="fixture-label">{t("journey.kicker")}</span>
        </div>
        <p>{t("journey.lead")}</p>
      </header>

      <div className="journey-grid">
        <section
          aria-labelledby="restaurant-title"
          className="restaurant-panel"
        >
          <header className="restaurant-header">
            <div>
              <h2 id="restaurant-title">
                {t(customerJourneyFixture.restaurant.nameKey)}
              </h2>
              <p>{t(customerJourneyFixture.restaurant.cuisineKey)}</p>
            </div>
            <span className="eta-badge">
              {t(customerJourneyFixture.restaurant.etaKey)}
            </span>
          </header>

          <h3 className="menu-heading">{t("journey.menu.title")}</h3>
          <ul className="menu-list">
            {customerJourneyFixture.menu.map((item) => {
              const itemName = t(item.nameKey);
              const quantity = state.quantities[item.id];
              return (
                <li
                  className="menu-item"
                  data-selected={quantity > 0}
                  key={item.id}
                >
                  <div className="dish-copy">
                    <strong>{itemName}</strong>
                    <span>{t(item.descriptionKey)}</span>
                  </div>
                  <div className="dish-action">
                    <span className="dish-price">
                      {formatMoney(item.priceMinor)}
                    </span>
                    <fieldset className="quantity-control">
                      <legend className="sr-only">{itemName}</legend>
                      <Button
                        aria-label={t("journey.quantity.decrease", {
                          item: itemName,
                        })}
                        className="quantity-button"
                        isDisabled={quantity === 0}
                        isIconOnly
                        onPress={() =>
                          dispatch({ type: "decrement", itemId: item.id })
                        }
                        size="sm"
                        variant="outline"
                      >
                        −
                      </Button>
                      <output aria-live="polite" className="quantity-value">
                        {quantity}
                      </output>
                      <Button
                        aria-label={t("journey.quantity.increase", {
                          item: itemName,
                        })}
                        className="quantity-button"
                        isIconOnly
                        onPress={() =>
                          dispatch({ type: "increment", itemId: item.id })
                        }
                        size="sm"
                        variant="outline"
                      >
                        +
                      </Button>
                    </fieldset>
                  </div>
                </li>
              );
            })}
          </ul>
        </section>

        <aside
          aria-label={t("journey.checkout.title")}
          className="journey-rail"
        >
          <section aria-labelledby="checkout-title" className="checkout-panel">
            <h2 id="checkout-title">{t("journey.checkout.title")}</h2>
            <dl className="checkout-breakdown">
              <div>
                <dt>{t("journey.checkout.items")}</dt>
                <dd>{formatMoney(subtotalMinor)}</dd>
              </div>
              <div>
                <dt>{t("journey.checkout.delivery")}</dt>
                <dd>
                  {formatMoney(customerJourneyFixture.checkout.deliveryMinor)}
                </dd>
              </div>
              <div>
                <dt>{t("journey.checkout.service")}</dt>
                <dd>
                  {formatMoney(customerJourneyFixture.checkout.serviceMinor)}
                </dd>
              </div>
              <div className="checkout-total">
                <dt>{t("journey.checkout.total")}</dt>
                <dd data-testid="journey-total">{formatMoney(totalMinor)}</dd>
              </div>
            </dl>

            <div className="checkout-detail">
              <span>{t("journey.checkout.address")}</span>
              <strong>{t(customerJourneyFixture.checkout.addressKey)}</strong>
            </div>
            <div className="checkout-detail">
              <span>{t("journey.checkout.payment")}</span>
              <strong>{t(customerJourneyFixture.checkout.paymentKey)}</strong>
            </div>

            <Button fullWidth onPress={() => setReviewed(true)}>
              {t("journey.checkout.action")}
            </Button>
            <span aria-live="polite" className="checkout-feedback">
              {reviewed ? t("journey.checkout.ready") : ""}
            </span>
          </section>

          <section aria-labelledby="order-title" className="order-panel">
            <div className="order-heading">
              <div>
                <span>{t("journey.order.eyebrow")}</span>
                <h2 id="order-title">{t("journey.order.title")}</h2>
              </div>
              <strong>{t("journey.order.eta")}</strong>
            </div>

            <div className="delivery-progress">
              <ol aria-label={t("journey.order.progress")}>
                {customerJourneyFixture.deliveryStages.map((stage, index) => (
                  <li data-complete={index <= activeStageIndex} key={stage.id}>
                    <span aria-hidden="true" />
                  </li>
                ))}
              </ol>
              <div aria-live="polite" className="active-stage">
                <strong>{t(activeStage.labelKey)}</strong>
                <span>{t(activeStage.detailKey)}</span>
              </div>
            </div>

            <div className="courier-row">
              <span aria-hidden="true" className="courier-avatar">
                {customerJourneyFixture.courier.initials}
              </span>
              <div>
                <span>{t("journey.courier.role")}</span>
                <strong>{t(customerJourneyFixture.courier.nameKey)}</strong>
                <small>{t(customerJourneyFixture.courier.transportKey)}</small>
              </div>
            </div>

            <Button
              fullWidth
              isDisabled={
                activeStageIndex ===
                customerJourneyFixture.deliveryStages.length - 1
              }
              onPress={showNextStage}
              variant="outline"
            >
              {t("journey.order.nextStage")}
            </Button>
          </section>
        </aside>
      </div>
    </section>
  );
}
