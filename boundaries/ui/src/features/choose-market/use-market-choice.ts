import { type Market, useMarketSelection } from "../../shared/market/market";

export type { Market };

export type MarketChoice =
  | { kind: "loading" }
  | { kind: "error"; retry: () => void }
  | {
      kind: "loaded";
      markets: readonly Market[];
      // The chosen Market, undefined until the viewer picks one that the
      // public gateway lists: a stored id of a Market that is gone is no
      // choice.
      chosen: Market | undefined;
      // Id the viewer chose earlier that is no longer listed.
      unlisted: string | undefined;
      choose: (marketId: string) => void;
    };

// Markets come from GET /markets of the public gateway (ADR 41), loaded once
// by MarketProvider.
export function useMarketChoice(): MarketChoice {
  const { marketId, choose, markets: list } = useMarketSelection();
  if (list.kind !== "loaded") return list;
  const chosen = list.markets.find((market) => market.id === marketId);
  return {
    kind: "loaded",
    markets: list.markets,
    chosen,
    unlisted: marketId !== undefined && !chosen ? marketId : undefined,
    choose,
  };
}
