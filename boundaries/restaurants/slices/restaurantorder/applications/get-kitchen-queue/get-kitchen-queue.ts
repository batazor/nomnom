import type { RestaurantOrderState } from "../../domains/restaurantorder/restaurantorder.ts";
import type { KitchenStaffAccess } from "../access-control.ts";
import {
  authorizeKitchenBoard,
  type KitchenBoardItem,
  type KitchenBoardQuery,
} from "../kitchen-board.ts";

export type GetKitchenQueue = KitchenBoardQuery;

export type KitchenQueueEntry = Readonly<{
  restaurantOrderId: string;
  orderId: string;
  state: RestaurantOrderState;
  items: readonly KitchenBoardItem[];
  acceptedAt: Date | undefined;
  queuedAt: Date | undefined;
  expectedReadyAt: Date | undefined;
  /** Version of the Restaurant Order the entry was read at. */
  version: number;
}>;

export type KitchenQueueResult = Readonly<{
  restaurantId: string;
  entries: readonly KitchenQueueEntry[];
}>;

/** Projection port: Restaurant Orders of the restaurant in `QUEUED`. */
export interface KitchenQueueReader {
  listQueued(restaurantId: string): Promise<readonly KitchenQueueEntry[]>;
}

/**
 * Kitchen Queue policy: the recommended order of queued Restaurant Orders.
 * The scheduling algorithm (FIFO, priority, courier ETA, manual order) is an
 * open product decision and lives behind this port.
 */
export interface KitchenQueueOrdering {
  order(entries: readonly KitchenQueueEntry[]): readonly KitchenQueueEntry[];
}

/** Returns queued orders in the order given by the Kitchen Queue policy. */
export class GetKitchenQueueHandler {
  readonly #accessControl: KitchenStaffAccess;
  readonly #reader: KitchenQueueReader;
  readonly #ordering: KitchenQueueOrdering;

  constructor(dependencies: {
    accessControl: KitchenStaffAccess;
    reader: KitchenQueueReader;
    ordering: KitchenQueueOrdering;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#reader = dependencies.reader;
    this.#ordering = dependencies.ordering;
  }

  async execute(query: GetKitchenQueue): Promise<KitchenQueueResult> {
    await authorizeKitchenBoard(
      this.#accessControl,
      query,
      "view kitchen queue",
    );
    const entries = await this.#reader.listQueued(query.restaurantId);
    return {
      restaurantId: query.restaurantId,
      entries: this.#ordering.order(entries),
    };
  }
}
