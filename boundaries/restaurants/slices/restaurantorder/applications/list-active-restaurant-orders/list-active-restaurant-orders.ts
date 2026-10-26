import type { RestaurantOrderState } from "../../domains/restaurantorder/restaurantorder.ts";
import type { KitchenStaffAccess } from "../access-control.ts";
import {
  authorizeKitchenBoard,
  type KitchenBoardItem,
  type KitchenBoardQuery,
} from "../kitchen-board.ts";

export type ListActiveRestaurantOrders = KitchenBoardQuery;

export type ActiveRestaurantOrder = Readonly<{
  restaurantOrderId: string;
  orderId: string;
  state: RestaurantOrderState;
  items: readonly KitchenBoardItem[];
  /** Unset on orders received before the timeline recorded it. */
  receivedAt: Date | undefined;
  /** Time of Acceptance; unset before it (`RECEIVED`). */
  acceptedAt: Date | undefined;
  /** Unset until Acceptance; includes reported Preparation Delays. */
  expectedReadyAt: Date | undefined;
  /** Cook assigned by Start Preparation; unset before it. */
  cookId: string | undefined;
  /** Version of the Restaurant Order the entry was read at. */
  version: number;
}>;

export type ActiveRestaurantOrdersResult = Readonly<{
  restaurantId: string;
  orders: readonly ActiveRestaurantOrder[];
}>;

/**
 * Read model port of the kitchen board: orders of the restaurant in
 * `RECEIVED`, `QUEUED`, `PREPARING`, `ASSEMBLED` and `READY`, by ascending
 * `received_at`, then by id.
 */
export interface ActiveRestaurantOrdersReader {
  listActiveOrders(
    restaurantId: string,
  ): Promise<readonly ActiveRestaurantOrder[]>;
}

/** Returns every order the kitchen still works on, from receipt to Pickup. */
export class ListActiveRestaurantOrdersHandler {
  readonly #accessControl: KitchenStaffAccess;
  readonly #reader: ActiveRestaurantOrdersReader;

  constructor(dependencies: {
    accessControl: KitchenStaffAccess;
    reader: ActiveRestaurantOrdersReader;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#reader = dependencies.reader;
  }

  async execute(
    query: ListActiveRestaurantOrders,
  ): Promise<ActiveRestaurantOrdersResult> {
    await authorizeKitchenBoard(
      this.#accessControl,
      query,
      "list active restaurant orders",
    );
    return {
      restaurantId: query.restaurantId,
      orders: await this.#reader.listActiveOrders(query.restaurantId),
    };
  }
}
