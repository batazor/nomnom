import type { KitchenOrdersInStatesReader } from "../../restaurantorder/applications/kitchen-board/kitchen-board.ts";
import type {
  PendingKitchenOrder,
  PendingKitchenOrdersReader,
} from "../applications/simulate-kitchen/simulate-kitchen.ts";
import { SIMULATED_STATES } from "../domains/simulator/simulator.ts";

/** Orders waiting for the kitchen, read from the board of `restaurantorder`. */
export class BoardPendingKitchenOrders implements PendingKitchenOrdersReader {
  readonly #board: KitchenOrdersInStatesReader;

  constructor(board: KitchenOrdersInStatesReader) {
    this.#board = board;
  }

  listPendingKitchenOrders(): Promise<readonly PendingKitchenOrder[]> {
    return this.#board.listKitchenOrdersInStates(SIMULATED_STATES);
  }
}
