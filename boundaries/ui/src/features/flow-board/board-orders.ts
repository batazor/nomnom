import { useCallback, useState } from "react";

// Orders placed from this board, newest first. Only ids are kept in the
// browser: everything else is read from BFF.
const storageKey = "nomnom.board.orders.v1";
const maxOrders = 20;

export function parseBoardOrders(raw: string | null): string[] {
  try {
    const value: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(value)
      ? value
          .filter((id): id is string => typeof id === "string")
          .slice(0, maxOrders)
      : [];
  } catch {
    return [];
  }
}

function readOrders(): string[] {
  try {
    return parseBoardOrders(localStorage.getItem(storageKey));
  } catch {
    return [];
  }
}

function writeOrders(ids: readonly string[]): void {
  try {
    localStorage.setItem(storageKey, JSON.stringify(ids));
  } catch {
    // Without storage the list lives as long as the page.
  }
}

export function useBoardOrders() {
  const [ids, setIds] = useState<string[]>(readOrders);

  const add = useCallback((orderId: string) => {
    setIds((current) => {
      const next = [orderId, ...current.filter((id) => id !== orderId)].slice(
        0,
        maxOrders,
      );
      writeOrders(next);
      return next;
    });
  }, []);

  // Drops an order BFF no longer knows, so the strip does not wait for it.
  const remove = useCallback((orderId: string) => {
    setIds((current) => {
      const next = current.filter((id) => id !== orderId);
      writeOrders(next);
      return next;
    });
  }, []);

  return { ids, add, remove };
}
