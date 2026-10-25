import {
  createContext,
  createElement,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react";

// The board refreshes the work of every column this often while the page is
// visible, so a step made outside the board still shows up.
export const boardPollIntervalMs = 3_000;

type BoardRefresh = Readonly<{ version: number; bump: () => void }>;

const BoardRefreshContext = createContext<BoardRefresh>({
  version: 0,
  bump: () => {},
});

// A step in one column refreshes the others at once instead of waiting for
// the next poll.
export function BoardRefreshProvider({ children }: { children: ReactNode }) {
  const [version, setVersion] = useState(0);
  const bump = useCallback(() => setVersion((current) => current + 1), []);
  return createElement(
    BoardRefreshContext.Provider,
    { value: { version, bump } },
    children,
  );
}

export function useBoardRefresh(): () => void {
  return useContext(BoardRefreshContext).bump;
}

// Calls refresh every intervalMs while the document is visible and enabled,
// and right after any step on the board.
export function usePolling(
  refresh: () => void,
  enabled = true,
  intervalMs = boardPollIntervalMs,
) {
  const latest = useRef(refresh);
  latest.current = refresh;
  const { version } = useContext(BoardRefreshContext);

  useEffect(() => {
    if (enabled && version > 0) latest.current();
  }, [enabled, version]);

  useEffect(() => {
    if (!enabled) return;
    const timer = setInterval(() => {
      if (document.visibilityState === "visible") latest.current();
    }, intervalMs);
    return () => clearInterval(timer);
  }, [enabled, intervalMs]);
}
