import type { RestaurantOrderRepository } from "../../../domains/restaurantorder/restaurantorder.ts";
import type { KitchenStaffAccess } from "../../access-control/access-control.ts";
import type { Clock } from "../../clock/clock.ts";
import { PermissionDeniedError } from "../../failure/failure.ts";
import type { KitchenCommand } from "../../kitchen-command/kitchen-command.ts";
import { loadRestaurantOrder } from "../../load/load.ts";
import {
  type RestaurantOrderView,
  toRestaurantOrderView,
} from "../../restaurant-order-view/restaurant-order-view.ts";

export type MarkAssembled = KitchenCommand;

/**
 * Kitchen confirms that all required items are prepared and assembled.
 * `RestaurantOrderAssembled` is local and a single document is written, so no
 * Unit of Work or Outbox is used. A repeat is rejected as already in state.
 */
export class MarkAssembledHandler {
  readonly #accessControl: KitchenStaffAccess;
  readonly #restaurantOrders: RestaurantOrderRepository;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: KitchenStaffAccess;
    restaurantOrders: RestaurantOrderRepository;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#restaurantOrders = dependencies.restaurantOrders;
    this.#clock = dependencies.clock;
  }

  async execute(command: MarkAssembled): Promise<RestaurantOrderView> {
    if (
      !(await this.#accessControl.canOperateKitchen(
        command.subject,
        command.restaurantId,
      ))
    ) {
      throw new PermissionDeniedError("mark restaurant order assembled");
    }
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
