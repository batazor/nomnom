import { useQuery } from "urql";

import {
  ChooseMarketDocument,
  type ChooseMarketQuery,
} from "../../shared/graphql/generated/graphql";
import { useMarketSelection } from "../../shared/market/market";

export type Market = ChooseMarketQuery["markets"][number];

export type MarketChoice =
  | { kind: "loading" }
  | { kind: "error"; retry: () => void }
  | {
      kind: "loaded";
      markets: readonly Market[];
      // The chosen Market, undefined until the viewer picks one that BFF
      // lists: a stored id of a Market that is gone is no choice.
      chosen: Market | undefined;
      // Id the viewer chose earlier that BFF no longer lists.
      unlisted: string | undefined;
      choose: (marketId: string) => void;
    };

// Markets come from BFF once per client (no polling, ADR 36); the query
// needs no X-Nomnom-Market header.
export function useMarketChoice(): MarketChoice {
  const { marketId, choose } = useMarketSelection();
  const [query, reexecute] = useQuery({ query: ChooseMarketDocument });

  if (query.fetching && !query.data) return { kind: "loading" };
  if (query.error || !query.data) {
    return {
      kind: "error",
      retry: () => reexecute({ requestPolicy: "network-only" }),
    };
  }
  const markets = query.data.markets;
  const chosen = markets.find((market) => market.id === marketId);
  return {
    kind: "loaded",
    markets,
    chosen,
    unlisted: marketId !== undefined && !chosen ? marketId : undefined,
    choose,
  };
}
