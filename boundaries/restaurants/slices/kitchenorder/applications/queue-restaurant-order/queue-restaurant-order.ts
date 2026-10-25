import type { Clock } from "../../../../pkg/application/clock.ts";
import { NotFoundError } from "../../../../pkg/application/errors.ts";
import {
  AlreadyInStateError,
  type RestaurantOrderRepository,
} from "../../domains/restaurantorder/restaurantorder.ts";
import {
  type RestaurantOrderView,
  toRestaurantOrderView,
} from "../restaurant-order-support.ts";

export type QueueRestaurantOrder = Readonly<{ restaurantOrderId: string }>;

/**
 * System command of `restaurants` for deferred queuing of an `ACCEPTED`
 * order. In the MVP Accept Order queues in the same Unit of Work, so this
 * handler covers orders left in `ACCEPTED`. It has no external entrypoint and
 * no subject; the fact is local and a single document is written. A repeat for
 * a `QUEUED` order succeeds without changes.
 */
export class QueueRestaurantOrderHandler {
  readonly #restaurantOrders: RestaurantOrderRepository;
  readonly #clock: Clock;

  constructor(dependencies: {
    restaurantOrders: RestaurantOrderRepository;
    clock: Clock;
  }) {
    this.#restaurantOrders = dependencies.restaurantOrders;
    this.#clock = dependencies.clock;
  }

  async execute(command: QueueRestaurantOrder): Promise<RestaurantOrderView> {
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
