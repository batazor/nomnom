import {
  executeKitchenCommand,
  type KitchenCommand,
  type KitchenCommandDependencies,
  type RestaurantOrderView,
} from "../restaurant-order-support.ts";

export type RejectOrder = KitchenCommand &
  Readonly<{
    /** Optional opaque reason; a reason catalog is not agreed yet. */
    reason?: string;
  }>;

/**
 * Rejects a `RECEIVED` order and publishes `RestaurantOrderRejected`.
 * Rejection after Acceptance is not allowed until a product decision.
 */
export class RejectOrderHandler {
  readonly #dependencies: KitchenCommandDependencies;

  constructor(dependencies: KitchenCommandDependencies) {
    this.#dependencies = dependencies;
  }

  execute(command: RejectOrder): Promise<RestaurantOrderView> {
    return executeKitchenCommand(this.#dependencies, command, {
      action: "reject restaurant order",
      published: ["RestaurantOrderRejected"],
      change: (order, now) =>
        order.reject({ rejectedAt: now, reason: command.reason }),
    });
  }
}
