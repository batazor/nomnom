import {
  executeKitchenCommand,
  type KitchenCommand,
  type KitchenCommandDependencies,
  type RestaurantOrderView,
} from "../restaurant-order-support.ts";

export type ReportPreparationFailure = KitchenCommand &
  Readonly<{
    /** Required opaque reason, for example `ITEM_UNAVAILABLE`. */
    reason: string;
  }>;

/**
 * Moves a `PREPARING` or `ASSEMBLED` order to `FAILED` and publishes
 * `RestaurantOrderFailed` (Integration Event candidate `PreparationFailed`).
 */
export class ReportPreparationFailureHandler {
  readonly #dependencies: KitchenCommandDependencies;

  constructor(dependencies: KitchenCommandDependencies) {
    this.#dependencies = dependencies;
  }

  execute(command: ReportPreparationFailure): Promise<RestaurantOrderView> {
    return executeKitchenCommand(this.#dependencies, command, {
      action: "report preparation failure",
      published: ["RestaurantOrderFailed"],
      change: (order, now) =>
        order.reportFailure({ failedAt: now, reason: command.reason }),
    });
  }
}
