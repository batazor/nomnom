import type { RestaurantOrderState } from "../../domains/restaurantorder/restaurantorder.ts";
import type { KitchenAccessControl } from "../access-control.ts";
import {
  authorizeKitchenBoard,
  type KitchenBoardItem,
  type KitchenBoardQuery,
} from "../kitchen-board.ts";

export type ListActivePreparations = KitchenBoardQuery;

export type ActivePreparation = Readonly<{
  restaurantOrderId: string;
  orderId: string;
  state: RestaurantOrderState;
  items: readonly KitchenBoardItem[];
  preparationStartedAt: Date | undefined;
  /** Current expected time, including reported Preparation Delays. */
  expectedReadyAt: Date | undefined;
}>;

export type ActivePreparationsResult = Readonly<{
  restaurantId: string;
  preparations: readonly ActivePreparation[];
}>;

/** Read model port of the kitchen board: orders in `PREPARING` and `ASSEMBLED`. */
export interface ActivePreparationsReader {
  listActive(restaurantId: string): Promise<readonly ActivePreparation[]>;
}

export class ListActivePreparationsHandler {
  readonly #accessControl: KitchenAccessControl;
  readonly #reader: ActivePreparationsReader;

  constructor(dependencies: {
    accessControl: KitchenAccessControl;
    reader: ActivePreparationsReader;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#reader = dependencies.reader;
  }

  async execute(
    query: ListActivePreparations,
  ): Promise<ActivePreparationsResult> {
    await authorizeKitchenBoard(
      this.#accessControl,
      query,
      "list active preparations",
    );
    return {
      restaurantId: query.restaurantId,
      preparations: await this.#reader.listActive(query.restaurantId),
    };
  }
}
