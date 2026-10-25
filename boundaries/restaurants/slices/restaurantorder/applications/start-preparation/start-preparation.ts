import {
  executeKitchenCommand,
  type KitchenCommand,
  type KitchenCommandDependencies,
  type RepeatPolicy,
} from "../kitchen-command.ts";
import type { RestaurantOrderView } from "../restaurant-order-view.ts";

export type StartPreparation = KitchenCommand;

/**
 * Starts Preparation of a `QUEUED` order and publishes `PreparationStarted`.
 * A repeat for a `PREPARING` order follows the explicit {@link RepeatPolicy}.
 */
export class StartPreparationHandler {
  readonly #dependencies: KitchenCommandDependencies;
  readonly #onRepeat: RepeatPolicy;

  constructor(
    dependencies: KitchenCommandDependencies & { onRepeat: RepeatPolicy },
  ) {
    this.#dependencies = dependencies;
    this.#onRepeat = dependencies.onRepeat;
  }

  execute(command: StartPreparation): Promise<RestaurantOrderView> {
    return executeKitchenCommand(this.#dependencies, command, {
      action: "start preparation",
      published: ["PreparationStarted"],
      change: (order, now) => order.startPreparation({ startedAt: now }),
      repeatPolicy: this.#onRepeat,
    });
  }
}
