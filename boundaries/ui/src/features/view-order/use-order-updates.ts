import { useCallback, useEffect, useState } from "react";
import { useQuery, useSubscription } from "urql";

import {
  OrderChangedDocument,
  type ViewedOrderFragment,
  ViewOrderDocument,
} from "../../shared/graphql/generated/graphql";
import { statusPresentation } from "./order-presentation";

export type ViewedOrder = ViewedOrderFragment;

// One order: the query loads it, orderChanged pushes it after every change
// (ADR 36) until it is final. No polling; the client refetches the query on
// every (re)connect of the stream, so changes in between are not lost.
export function useOrderUpdates(orderId: string) {
  const [result, reexecute] = useQuery({
    query: ViewOrderDocument,
    variables: { id: orderId },
    requestPolicy: "network-only",
  });
  const [order, setOrder] = useState<ViewedOrder | null | undefined>(undefined);

  useEffect(() => {
    // A failed load keeps the last order: data.order is null next to errors.
    if (!result.fetching && !result.error && result.data) {
      setOrder(result.data.order);
    }
  }, [result.fetching, result.error, result.data]);

  const final =
    order === null || (order ? statusPresentation[order.status].final : false);

  const [updates, resubscribe] = useSubscription({
    query: OrderChangedDocument,
    variables: { id: orderId },
    pause: order === undefined || final,
  });

  useEffect(() => {
    if (updates.data) setOrder(updates.data.orderChanged);
  }, [updates.data]);

  const refresh = useCallback(() => {
    reexecute({ requestPolicy: "network-only" });
    if (!final) resubscribe();
  }, [reexecute, resubscribe, final]);

  return {
    order,
    failed:
      (!result.fetching && Boolean(result.error)) || Boolean(updates.error),
    refresh,
  };
}
