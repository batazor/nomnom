import type { KitchenStaffAccess } from "../../access-control/access-control.ts";
import {
  authorizeKitchenBoard,
  type KitchenBoardQuery,
} from "../kitchen-board/kitchen-board.ts";

export type ListReadyOrders = KitchenBoardQuery;

export type ReadyOrder = Readonly<{
  restaurantOrderId: string;
  orderId: string;
  readyAt: Date | undefined;
}>;

export type ReadyOrdersResult = Readonly<{
  restaurantId: string;
  orders: readonly ReadyOrder[];
}>;

/**
 * Read model port of the kitchen board: orders in `READY`. Courier data and
 * Pickup are not projected yet; they will come from `delivery` events.
 */
export interface ReadyOrdersReader {
  listReady(restaurantId: string): Promise<readonly ReadyOrder[]>;
}

export class ListReadyOrdersHandler {
  readonly #accessControl: KitchenStaffAccess;
  readonly #reader: ReadyOrdersReader;

  constructor(dependencies: {
    accessControl: KitchenStaffAccess;
    reader: ReadyOrdersReader;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#reader = dependencies.reader;
  }

  async execute(query: ListReadyOrders): Promise<ReadyOrdersResult> {
    await authorizeKitchenBoard(
      this.#accessControl,
      query,
      "list ready orders",
    );
    return {
      restaurantId: query.restaurantId,
      orders: await this.#reader.listReady(query.restaurantId),
    };
  }
}
