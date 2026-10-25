import { useCallback, useEffect, useState } from "react";
import { useQuery } from "urql";

import {
  ViewOrderDocument,
  type ViewOrderQuery,
} from "../../shared/graphql/generated/graphql";
import { statusPresentation } from "./order-presentation";

export type ViewedOrder = NonNullable<ViewOrderQuery["order"]>;

export const pollIntervalMs = 10_000;
export const maxPollIntervalMs = 60_000;

function isDocumentVisible(): boolean {
  return document.visibilityState !== "hidden";
}

export function nextPollDelay(failures: number): number {
  return Math.min(pollIntervalMs * 2 ** failures, maxPollIntervalMs);
}

// Bounded polling of one order: only while the document is visible and the
// order is not final; transport errors back off up to maxPollIntervalMs.
export function useOrderPolling(orderId: string) {
  const [result, reexecute] = useQuery({
    query: ViewOrderDocument,
    variables: { id: orderId },
    requestPolicy: "network-only",
  });
  const [order, setOrder] = useState<ViewedOrder | null | undefined>(undefined);
  const [failures, setFailures] = useState(0);
  const [visible, setVisible] = useState(isDocumentVisible);

  const refresh = useCallback(
    () => reexecute({ requestPolicy: "network-only" }),
    [reexecute],
  );

  const { fetching, error, data } = result;

  useEffect(() => {
    if (fetching) return;
    if (error) {
      setFailures((count) => count + 1);
    } else if (data) {
      setFailures(0);
      setOrder(data.order);
    }
  }, [fetching, error, data]);

  const stopped =
    order === null || (order ? statusPresentation[order.status].final : false);

  useEffect(() => {
    function onVisibilityChange() {
      const nowVisible = isDocumentVisible();
      setVisible(nowVisible);
      if (nowVisible && !stopped) refresh();
    }
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () =>
      document.removeEventListener("visibilitychange", onVisibilityChange);
  }, [refresh, stopped]);

  useEffect(() => {
    if (fetching || stopped || !visible) return;
    const timer = setTimeout(refresh, nextPollDelay(failures));
    return () => clearTimeout(timer);
  }, [fetching, stopped, visible, failures, refresh]);

  return {
    order,
    failed: !fetching && Boolean(error),
    refresh,
  };
}
