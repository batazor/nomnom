import type { ReactNode } from "react";

import { useI18n } from "../../shared/i18n/i18n";
import { Button } from "../../shared/ui";
import { useMarketChoice } from "./use-market-choice";
import "./choose-market.css";

// Market-scoped screens render only in a chosen Market that BFF lists;
// otherwise the viewer picks one. The first Market is never picked for them,
// not even when it is the only one.
export function MarketGate({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  const choice = useMarketChoice();

  if (choice.kind === "loading") {
    return (
      <p aria-busy="true" className="choose-market-message">
        {t("market.loading")}
      </p>
    );
  }
  if (choice.kind === "error") {
    return (
      <div className="choose-market-message" role="alert">
        <h1 className="choose-market-title">{t("market.error.title")}</h1>
        <p>{t("market.error.detail")}</p>
        <Button onClick={choice.retry} variant="outline">
          {t("market.retry")}
        </Button>
      </div>
    );
  }
  if (choice.chosen) return children;

  return (
    <section aria-labelledby="choose-market-title" className="choose-market">
      <h1 className="choose-market-title" id="choose-market-title">
        {t("market.choose.title")}
      </h1>
      {choice.markets.length === 0 ? (
        <p className="choose-market-detail">{t("market.empty")}</p>
      ) : (
        <>
          <p className="choose-market-detail">{t("market.choose.detail")}</p>
          {choice.unlisted ? (
            <p className="choose-market-detail" role="status">
              {t("market.choose.unavailable")}
            </p>
          ) : null}
          <ul className="choose-market-list">
            {choice.markets.map((market) => (
              <li key={market.id}>
                <Button
                  aria-label={t("market.choose.action", { name: market.name })}
                  fullWidth
                  onClick={() => choice.choose(market.id)}
                  variant="outline"
                >
                  {t("market.option", {
                    name: market.name,
                    currency: market.currency,
                  })}
                </Button>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

// Renders children only in a chosen Market that BFF lists, nothing else:
// for header parts whose operations are routed to a Cell.
export function WhenMarketChosen({ children }: { children: ReactNode }) {
  const choice = useMarketChoice();
  return choice.kind === "loaded" && choice.chosen ? children : null;
}
