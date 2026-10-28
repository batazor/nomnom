import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";

// Market the viewer chose (ADR 41): the public gateway routes
// /m/{marketId}/graphql to the Cell of this Market. Only the id is kept in
// the browser; the list of Markets always comes from the public gateway.
const storageKey = "nomnom.market.v1";

// Format of market_id (ADR 41); anything else in storage is not a choice.
const marketIdPattern = /^[a-z][a-z0-9-]{1,31}$/;

export function parseMarketId(raw: string | null): string | undefined {
  return raw !== null && marketIdPattern.test(raw) ? raw : undefined;
}

// GraphQL endpoint of the BFF of the Cell of a Market.
export function marketGraphqlUrl(apiUrl: string, marketId: string): string {
  return `${apiUrl}/m/${marketId}/graphql`;
}

function readMarketId(): string | undefined {
  try {
    return parseMarketId(localStorage.getItem(storageKey));
  } catch {
    return undefined;
  }
}

function writeMarketId(marketId: string): void {
  try {
    localStorage.setItem(storageKey, marketId);
  } catch {
    // Without storage the choice lives as long as the page.
  }
}

// timeZone is the IANA zone of the Market: windows and deadlines of its orders
// are shown in it, not in the zone of the browser.
export type Market = Readonly<{
  id: string;
  name: string;
  currency: string;
  timeZone: string;
}>;

export type MarketList =
  | { kind: "loading" }
  | { kind: "error"; retry: () => void }
  | { kind: "loaded"; markets: readonly Market[] };

type MarketSelection = Readonly<{
  // Base URL of the public API.
  apiUrl: string;
  // Id of the chosen Market; undefined until the viewer picks one.
  marketId: string | undefined;
  choose: (marketId: string) => void;
  markets: MarketList;
}>;

const MarketContext = createContext<MarketSelection | null>(null);

function isMarketList(body: unknown): body is { markets: Market[] } {
  if (typeof body !== "object" || body === null) return false;
  const markets = (body as { markets?: unknown }).markets;
  return (
    Array.isArray(markets) &&
    markets.every(
      (market) =>
        typeof market?.id === "string" &&
        typeof market?.name === "string" &&
        typeof market?.currency === "string" &&
        typeof market?.timeZone === "string",
    )
  );
}

// The Markets come from GET /markets of the public gateway once per page
// (no polling, ADR 36); retry asks again.
async function fetchMarkets(apiUrl: string, signal: AbortSignal) {
  const response = await fetch(`${apiUrl}/markets`, { signal });
  if (!response.ok) throw new Error(`markets: HTTP ${response.status}`);
  const body: unknown = await response.json();
  if (!isMarketList(body)) throw new Error("markets: unexpected response");
  return body.markets;
}

export function MarketProvider({
  apiUrl,
  children,
}: {
  apiUrl: string;
  children: ReactNode;
}) {
  const [marketId, setMarketId] = useState<string | undefined>(readMarketId);
  const [attempt, setAttempt] = useState(0);
  const [markets, setMarkets] = useState<MarketList>({ kind: "loading" });

  // Each attempt asks once; a retry starts the next attempt from loading.
  useEffect(() => {
    const abort = new AbortController();
    if (attempt > 0) setMarkets({ kind: "loading" });
    const retry = () => setAttempt((current) => current + 1);
    fetchMarkets(apiUrl, abort.signal).then(
      (list) => setMarkets({ kind: "loaded", markets: list }),
      () => {
        if (!abort.signal.aborted) setMarkets({ kind: "error", retry });
      },
    );
    return () => abort.abort();
  }, [apiUrl, attempt]);

  const choose = useCallback((next: string) => {
    if (!marketIdPattern.test(next)) {
      throw new Error(`Invalid market id: ${next}`);
    }
    writeMarketId(next);
    setMarketId(next);
  }, []);

  const value = useMemo(
    () => ({ apiUrl, marketId, choose, markets }),
    [apiUrl, marketId, choose, markets],
  );

  return (
    <MarketContext.Provider value={value}>{children}</MarketContext.Provider>
  );
}

export function useMarketSelection(): MarketSelection {
  const selection = useContext(MarketContext);
  if (!selection) throw new Error("useMarketSelection needs MarketProvider");
  return selection;
}

// Time zone of the chosen Market; null while the list of Markets loads.
// Market-scoped screens render behind MarketGate, so a loaded list without
// the chosen Market is a defect, not a state to show.
export function useMarketTimeZone(): string | null {
  const { marketId, markets } = useMarketSelection();
  if (markets.kind !== "loaded") return null;
  const market = markets.markets.find((candidate) => candidate.id === marketId);
  if (!market) throw new Error("useMarketTimeZone needs a chosen Market");
  return market.timeZone;
}
