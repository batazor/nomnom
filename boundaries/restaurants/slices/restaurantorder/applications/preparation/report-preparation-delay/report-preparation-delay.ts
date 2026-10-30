import {
  executeKitchenCommand,
  type KitchenCommand,
  type KitchenCommandDependencies,
} from "../../kitchen-command/kitchen-command.ts";
import type { RestaurantOrderView } from "../../restaurant-order-view/restaurant-order-view.ts";

export type ReportPreparationDelay = KitchenCommand &
  Readonly<{
    /** New expected ready time; must be later than the current one and now. */
    expectedReadyAt: Date;
    /** Optional opaque category for analytics; it does not affect transitions. */
    reason?: string;
  }>;

/**
 * Moves `expected_ready_at` later without changing the State and publishes
 * `PreparationDelayed` with the previous and the new time.
 */
export class ReportPreparationDelayHandler {
  readonly #dependencies: KitchenCommandDependencies;

  constructor(dependencies: KitchenCommandDependencies) {
    this.#dependencies = dependencies;
  }

  execute(command: ReportPreparationDelay): Promise<RestaurantOrderView> {
    return executeKitchenCommand(this.#dependencies, command, {
      action: "report preparation delay",
      published: ["PreparationDelayed"],
      change: (order, now) =>
        order.reportDelay({
          reportedAt: now,
          expectedReadyAt: command.expectedReadyAt,
          reason: command.reason,
        }),
    });
  }
}
