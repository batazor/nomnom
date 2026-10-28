import type { Clock } from "../../../../pkg/application/clock.ts";
import type {
  OrderPriority,
  OrderScheduleValue,
  RestaurantOrderState,
} from "../../domains/restaurantorder/restaurantorder.ts";
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
  priority: OrderPriority;
  items: readonly KitchenBoardItem[];
  acceptedAt: Date | undefined;
  queuedAt: Date | undefined;
  expectedReadyAt: Date | undefined;
  /** Delivery Window and Planned Ready At of a scheduled order (FR-241). */
  schedule: OrderScheduleValue | undefined;
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
 * Kitchen Queue policy (FR-241): the order in which Start Next Preparation
 * takes queued Restaurant Orders of a restaurant at `now`.
 */
export interface KitchenQueueOrdering {
  order(
    restaurantId: string,
    entries: readonly KitchenQueueEntry[],
    now: Date,
  ): Promise<readonly KitchenQueueEntry[]>;
}

/** Returns queued orders in the order Start Next Preparation takes them. */
export class GetKitchenQueueHandler {
  readonly #accessControl: KitchenStaffAccess;
  readonly #reader: KitchenQueueReader;
  readonly #ordering: KitchenQueueOrdering;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: KitchenStaffAccess;
    reader: KitchenQueueReader;
    ordering: KitchenQueueOrdering;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#reader = dependencies.reader;
    this.#ordering = dependencies.ordering;
    this.#clock = dependencies.clock;
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
      entries: await this.#ordering.order(
        query.restaurantId,
        entries,
        this.#clock.now(),
      ),
    };
  }
}
