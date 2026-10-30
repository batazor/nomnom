import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { Clock } from "../../../../pkg/usecase/clock.ts";
import { NotFoundError } from "../../../../pkg/usecase/errors.ts";
import {
  AlreadyInStateError,
  type RestaurantOrderRepository,
} from "../../domains/restaurantorder/restaurantorder.ts";
import type { FulfillmentProcessAccess } from "../access-control/access-control.ts";
import {
  type RestaurantOrderView,
  toRestaurantOrderView,
} from "../restaurant-order-view/restaurant-order-view.ts";

export type QueueRestaurantOrder = Readonly<{
  /** `service` identity of the `orders` fulfillment process. */
  subject: Subject;
  restaurantOrderId: string;
}>;

/**
 * Moves an `ACCEPTED` order to the Kitchen Queue (FR-224): the workflow of
 * `orders` queues a scheduled order at its planned start. Only the `service`
 * identity of `orders` may call it; `expected_ready_at` does not change. The
 * fact is local and a single document is written; the save notifies the
 * kitchen board (ADR 36). A repeat for a `QUEUED` order succeeds without
 * changes.
 */
export class QueueRestaurantOrderHandler {
  readonly #accessControl: FulfillmentProcessAccess;
  readonly #restaurantOrders: RestaurantOrderRepository;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: FulfillmentProcessAccess;
    restaurantOrders: RestaurantOrderRepository;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#restaurantOrders = dependencies.restaurantOrders;
    this.#clock = dependencies.clock;
  }

  async execute(command: QueueRestaurantOrder): Promise<RestaurantOrderView> {
    await requirePermission(
      this.#accessControl.canCoordinateFulfillment(command.subject),
      "queue restaurant order",
    );
    const order = await this.#restaurantOrders.get(command.restaurantOrderId);
    if (order === undefined) {
      throw new NotFoundError("RestaurantOrder", command.restaurantOrderId);
    }
    try {
      order.queue({ queuedAt: this.#clock.now() });
    } catch (error) {
      if (error instanceof AlreadyInStateError) {
        return toRestaurantOrderView(order);
      }
      throw error;
    }
    const version = await this.#restaurantOrders.save(order);
    order.pullEvents();
    return toRestaurantOrderView(order, version);
  }
}
