import { useCallback, useMemo, useRef, useState } from "react";

// A cart line as the customer chose it; prices always come from the menu.
export type CartLine = Readonly<{ menuItemId: string; quantity: number }>;

// Input sanity limit for one line, not a business rule: the order is checked
// by the backend when it is placed.
export const maxQuantity = 99;

export function cartStorageKey(restaurantId: string): string {
  return `nomnom.cart.v1.${restaurantId}`;
}

function isCartLine(value: unknown): value is CartLine {
  if (typeof value !== "object" || value === null) return false;
  const { menuItemId, quantity } = value as Record<string, unknown>;
  return (
    typeof menuItemId === "string" &&
    menuItemId !== "" &&
    Number.isInteger(quantity) &&
    (quantity as number) >= 1 &&
    (quantity as number) <= maxQuantity
  );
}

// Reads what an earlier session stored; anything malformed is dropped.
export function parseCart(raw: string | null): CartLine[] {
  if (!raw) return [];
  try {
    const value: unknown = JSON.parse(raw);
    const lines = (value as { lines?: unknown } | null)?.lines;
    return Array.isArray(lines) ? lines.filter(isCartLine) : [];
  } catch {
    return [];
  }
}

// localStorage may be missing, full or blocked (private mode, disabled site
// data): then the cart lives only in memory for this page.
function readCart(restaurantId: string): CartLine[] {
  try {
    return parseCart(window.localStorage.getItem(cartStorageKey(restaurantId)));
  } catch {
    return [];
  }
}

function writeCart(restaurantId: string, lines: readonly CartLine[]): void {
  try {
    const key = cartStorageKey(restaurantId);
    if (lines.length === 0) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, JSON.stringify({ lines }));
  } catch {
    // The in-memory cart keeps working without storage.
  }
}

export function addLine(
  lines: readonly CartLine[],
  menuItemId: string,
): CartLine[] {
  const existing = lines.find((line) => line.menuItemId === menuItemId);
  if (!existing) return [...lines, { menuItemId, quantity: 1 }];
  return setLineQuantity(lines, menuItemId, existing.quantity + 1);
}

export function setLineQuantity(
  lines: readonly CartLine[],
  menuItemId: string,
  quantity: number,
): CartLine[] {
  const bounded = Math.min(Math.max(quantity, 1), maxQuantity);
  return lines.map((line) =>
    line.menuItemId === menuItemId ? { ...line, quantity: bounded } : line,
  );
}

export function removeLine(
  lines: readonly CartLine[],
  menuItemId: string,
): CartLine[] {
  return lines.filter((line) => line.menuItemId !== menuItemId);
}

// One cart per restaurant, kept in localStorage. Every change is written at
// once, so leaving the page right after a change (for example, navigating to
// the placed order) cannot lose it.
export function useCart(restaurantId: string) {
  const [lines, setLines] = useState<CartLine[]>(() => readCart(restaurantId));
  const latest = useRef(lines);

  const commit = useCallback(
    (change: (current: readonly CartLine[]) => CartLine[]) => {
      const next = change(latest.current);
      latest.current = next;
      writeCart(restaurantId, next);
      setLines(next);
    },
    [restaurantId],
  );

  return useMemo(
    () => ({
      lines,
      add: (menuItemId: string) =>
        commit((current) => addLine(current, menuItemId)),
      setQuantity: (menuItemId: string, quantity: number) =>
        commit((current) => setLineQuantity(current, menuItemId, quantity)),
      remove: (menuItemId: string) =>
        commit((current) => removeLine(current, menuItemId)),
      // Drops lines of items that are no longer on the menu.
      retain: (menuItemIds: ReadonlySet<string>) => {
        if (latest.current.every((line) => menuItemIds.has(line.menuItemId)))
          return;
        commit((current) =>
          current.filter((line) => menuItemIds.has(line.menuItemId)),
        );
      },
      clear: () => commit(() => []),
    }),
    [lines, commit],
  );
}

export type Cart = ReturnType<typeof useCart>;
