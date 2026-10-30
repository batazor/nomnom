import type {
  OrderPriority,
  OrderScheduleValue,
} from "../../domains/restaurantorder/restaurantorder.ts";
import type { KitchenStaffAccess } from "../access-control/access-control.ts";
import {
  authorizeKitchenBoard,
  type KitchenBoardItem,
  type KitchenBoardQuery,
} from "../kitchen-board/kitchen-board.ts";

export type ListScheduledOrders = KitchenBoardQuery;

export type ScheduledOrder = Readonly<{
  restaurantOrderId: string;
  orderId: string;
  items: readonly KitchenBoardItem[];
  schedule: OrderScheduleValue;
  priority: OrderPriority;
  /** Version of the Restaurant Order the entry was read at. */
  version: number;
}>;

export type ScheduledOrdersResult = Readonly<{
  restaurantId: string;
  orders: readonly ScheduledOrder[];
}>;

/**
 * Read model port of the kitchen board: accepted scheduled orders of the
 * restaurant waiting for their start (`ACCEPTED` with a schedule), by
 * ascending Planned Ready At, then by id.
 */
export interface ScheduledOrdersReader {
  listScheduled(restaurantId: string): Promise<readonly ScheduledOrder[]>;
}

/**
 * Returns the scheduled orders the kitchen accepted and `orders` has not
 * queued yet (FR-684); they are not in the Kitchen Queue until then.
 */
export class ListScheduledOrdersHandler {
  readonly #accessControl: KitchenStaffAccess;
  readonly #reader: ScheduledOrdersReader;

  constructor(dependencies: {
    accessControl: KitchenStaffAccess;
    reader: ScheduledOrdersReader;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#reader = dependencies.reader;
  }

  async execute(query: ListScheduledOrders): Promise<ScheduledOrdersResult> {
    await authorizeKitchenBoard(
      this.#accessControl,
      query,
      "list scheduled orders",
    );
    return {
      restaurantId: query.restaurantId,
      orders: await this.#reader.listScheduled(query.restaurantId),
    };
  }
}
