import {
  executeKitchenCommand,
  type KitchenCommand,
  type KitchenCommandDependencies,
  type RepeatPolicy,
} from "../kitchen-command.ts";
import type { RestaurantOrderView } from "../restaurant-order-view.ts";

export type StartPreparation = KitchenCommand;

/**
 * Cook Pool of the restaurant as seen by Start Preparation: assigns a free
 * cook on line inside the active Unit of Work and returns its id, or throws
 * `NoCookAvailableError`.
 */
export interface CookAssigner {
  assignFreeCook(restaurantId: string, at: Date): Promise<string>;
}

/**
 * Starts Preparation of a `QUEUED` order by a free cook on line and publishes
 * `PreparationStarted`; without a free cook the order stays `QUEUED` with
 * `NoCookAvailableError` (FR-225). A repeat for a `PREPARING` order follows
 * the explicit {@link RepeatPolicy} and assigns no cook.
 */
export class StartPreparationHandler {
  readonly #dependencies: KitchenCommandDependencies;
  readonly #cooks: CookAssigner;
  readonly #onRepeat: RepeatPolicy;

  constructor(
    dependencies: KitchenCommandDependencies & {
      cooks: CookAssigner;
      onRepeat: RepeatPolicy;
    },
  ) {
    this.#dependencies = dependencies;
    this.#cooks = dependencies.cooks;
    this.#onRepeat = dependencies.onRepeat;
  }

  execute(command: StartPreparation): Promise<RestaurantOrderView> {
    return executeKitchenCommand(this.#dependencies, command, {
      action: "start preparation",
      published: ["PreparationStarted"],
      change: async (order, now) => {
        order.checkStartPreparation();
        const cookId = await this.#cooks.assignFreeCook(
          order.restaurantId,
          now,
        );
        order.startPreparation({ startedAt: now, cookId });
      },
      repeatPolicy: this.#onRepeat,
    });
  }
}
