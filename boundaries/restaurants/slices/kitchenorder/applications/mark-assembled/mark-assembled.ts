import type { Clock } from "../../../../pkg/application/clock.ts";
import type { RestaurantOrderRepository } from "../../domains/restaurantorder/restaurantorder.ts";
import type { KitchenAccessControl } from "../access-control.ts";
import {
  authorize,
  type KitchenCommand,
  loadRestaurantOrder,
  type RestaurantOrderView,
  toRestaurantOrderView,
} from "../restaurant-order-support.ts";

export type MarkAssembled = KitchenCommand;

/**
 * Kitchen confirms that all required items are prepared and assembled.
 * `RestaurantOrderAssembled` is local and a single document is written, so no
 * Unit of Work or Outbox is used. A repeat is rejected as already in state.
 */
export class MarkAssembledHandler {
  readonly #accessControl: KitchenAccessControl;
  readonly #restaurantOrders: RestaurantOrderRepository;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: KitchenAccessControl;
    restaurantOrders: RestaurantOrderRepository;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#restaurantOrders = dependencies.restaurantOrders;
    this.#clock = dependencies.clock;
  }

  async execute(command: MarkAssembled): Promise<RestaurantOrderView> {
    await authorize(
      this.#accessControl.canOperateKitchen(
        command.subject,
        command.restaurantId,
      ),
      "mark restaurant order assembled",
    );
    const order = await loadRestaurantOrder(
      this.#restaurantOrders,
      command.restaurantId,
      command.restaurantOrderId,
    );
    order.markAssembled({ assembledAt: this.#clock.now() });
    const version = await this.#restaurantOrders.save(order);
    order.pullEvents();
    return toRestaurantOrderView(order, version);
  }
}
