import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useMemo,
  useState,
} from "react";

// Market the viewer chose (ADR 41): every BFF operation except `markets` is
// routed to the Cell of this Market by the X-Nomnom-Market header. Only the
// id is kept in the browser; the list of Markets always comes from BFF.
const storageKey = "nomnom.market.v1";

// Format of market_id (ADR 41); anything else in storage is not a choice.
const marketIdPattern = /^[a-z][a-z0-9-]{1,31}$/;

export function parseMarketId(raw: string | null): string | undefined {
  return raw !== null && marketIdPattern.test(raw) ? raw : undefined;
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

type MarketSelection = Readonly<{
  // Id of the chosen Market; undefined until the viewer picks one.
  marketId: string | undefined;
  choose: (marketId: string) => void;
}>;

const MarketContext = createContext<MarketSelection | null>(null);

export function MarketProvider({ children }: { children: ReactNode }) {
  const [marketId, setMarketId] = useState<string | undefined>(readMarketId);

  const choose = useCallback((next: string) => {
    if (!marketIdPattern.test(next)) {
      throw new Error(`Invalid market id: ${next}`);
    }
    writeMarketId(next);
    setMarketId(next);
  }, []);

  const value = useMemo(() => ({ marketId, choose }), [marketId, choose]);

  return (
    <MarketContext.Provider value={value}>{children}</MarketContext.Provider>
  );
}

export function useMarketSelection(): MarketSelection {
  const selection = useContext(MarketContext);
  if (!selection) throw new Error("useMarketSelection needs MarketProvider");
  return selection;
}
