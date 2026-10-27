import type {
  KitchenQueueEntry,
  KitchenQueueOrdering,
} from "../../applications/get-kitchen-queue/get-kitchen-queue.ts";

/**
 * MVP Kitchen Queue policy until the scheduling algorithm is decided: FIFO by
 * `accepted_at`, then by id. Priority, courier ETA and manual order remain an
 * open product decision behind {@link KitchenQueueOrdering}.
 */
export class FifoKitchenQueueOrdering implements KitchenQueueOrdering {
  order(entries: readonly KitchenQueueEntry[]): readonly KitchenQueueEntry[] {
    return [...entries].sort(
      (left, right) =>
        (left.acceptedAt?.getTime() ?? 0) -
          (right.acceptedAt?.getTime() ?? 0) ||
        (left.restaurantOrderId < right.restaurantOrderId ? -1 : 1),
    );
  }
}
