import type { KitchenStaffAccess } from "../../access-control/access-control.ts";
import {
  authorizeKitchenBoard,
  type KitchenBoardItem,
  type KitchenBoardQuery,
} from "../kitchen-board.ts";

export type ListIncomingOrders = KitchenBoardQuery;

export type IncomingOrder = Readonly<{
  restaurantOrderId: string;
  orderId: string;
  items: readonly KitchenBoardItem[];
  /** Unset on orders received before the timeline recorded it. */
  receivedAt: Date | undefined;
  /** Version of the Restaurant Order the entry was read at. */
  version: number;
}>;

export type IncomingOrdersResult = Readonly<{
  restaurantId: string;
  orders: readonly IncomingOrder[];
}>;

/**
 * Read model port of the kitchen board: orders of the restaurant in
 * `RECEIVED`, by ascending `received_at`, then by id.
 */
export interface IncomingOrdersReader {
  listIncoming(restaurantId: string): Promise<readonly IncomingOrder[]>;
}

/** Returns orders awaiting Accept or Reject by the kitchen (FR-586). */
export class ListIncomingOrdersHandler {
  readonly #accessControl: KitchenStaffAccess;
  readonly #reader: IncomingOrdersReader;

  constructor(dependencies: {
    accessControl: KitchenStaffAccess;
    reader: IncomingOrdersReader;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#reader = dependencies.reader;
  }

  async execute(query: ListIncomingOrders): Promise<IncomingOrdersResult> {
    await authorizeKitchenBoard(
      this.#accessControl,
      query,
      "list incoming orders",
    );
    return {
      restaurantId: query.restaurantId,
      orders: await this.#reader.listIncoming(query.restaurantId),
    };
  }
}
