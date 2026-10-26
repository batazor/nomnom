import { useI18n } from "../../shared/i18n/i18n";
import { SelectField } from "../../shared/ui";
import { useMarketChoice } from "./use-market-choice";

// Header switch of the Market; nothing while the list is not loaded.
export function MarketSwitcher() {
  const { t } = useI18n();
  const choice = useMarketChoice();
  if (choice.kind !== "loaded" || choice.markets.length === 0) return null;

  return (
    <SelectField
      className="market-switcher"
      label={t("market.label")}
      onChange={choice.choose}
      options={choice.markets.map((market) => ({
        value: market.id,
        label: market.name,
      }))}
      placeholder={t("market.placeholder")}
      value={choice.chosen?.id ?? ""}
    />
  );
}
