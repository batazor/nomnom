import {
  executeKitchenCommand,
  type KitchenCommand,
  type KitchenCommandDependencies,
  type RepeatPolicy,
} from "../../kitchen-command/kitchen-command.ts";
import type { RestaurantOrderView } from "../../restaurant-order-view/restaurant-order-view.ts";

export type MarkReadyForPickup = KitchenCommand;

/**
 * Marks an `ASSEMBLED` order ready for Pickup and publishes
 * `RestaurantOrderReady`; from `PREPARING` it is an invalid transition. A
 * repeat for a `READY` order follows the explicit {@link RepeatPolicy}.
 */
export class MarkReadyForPickupHandler {
  readonly #dependencies: KitchenCommandDependencies;
  readonly #onRepeat: RepeatPolicy;

  constructor(
    dependencies: KitchenCommandDependencies & { onRepeat: RepeatPolicy },
  ) {
    this.#dependencies = dependencies;
    this.#onRepeat = dependencies.onRepeat;
  }

  execute(command: MarkReadyForPickup): Promise<RestaurantOrderView> {
    return executeKitchenCommand(this.#dependencies, command, {
      action: "mark restaurant order ready",
      published: ["RestaurantOrderReady"],
      change: (order, now) => order.markReady({ readyAt: now }),
      repeatPolicy: this.#onRepeat,
    });
  }
}
