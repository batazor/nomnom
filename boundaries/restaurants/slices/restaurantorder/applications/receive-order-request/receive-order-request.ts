import type { Clock } from "../../../../pkg/application/clock.ts";
import {
  AlreadyExistsError,
  NotFoundError,
} from "../../../../pkg/application/errors.ts";
import type { IdGenerator } from "../../../../pkg/application/id-generator.ts";
import { requirePermission } from "../../../../pkg/application/require-permission.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import {
  DuplicateOrderIdError,
  type PreparationItemInput,
  RestaurantOrder,
  type RestaurantOrderRepository,
} from "../../domains/restaurantorder/restaurantorder.ts";
import type { FulfillmentCoordinatorAccess } from "../access-control.ts";
import {
  type RestaurantOrderView,
  toRestaurantOrderView,
} from "../restaurant-order-view.ts";

export type ReceiveOrderRequest = Readonly<{
  /** `service` identity of the `orders` fulfillment process. */
  subject: Subject;
  restaurantId: string;
  /** Contract `Order ID` from `orders`; one Restaurant Order per Order. */
  orderId: string;
  items: readonly PreparationItemInput[];
}>;

/** Whether a restaurant is registered; nothing else of it is needed here. */
export interface RestaurantExistence {
  exists(restaurantId: string): Promise<boolean>;
}

export type ReceiveOrderRequestResult = Readonly<{
  restaurantOrder: RestaurantOrderView;
  /** `false` when the request repeated an already received order. */
  created: boolean;
}>;

/**
 * Idempotently creates a `RECEIVED` Restaurant Order. A repeat with the same
 * composition returns the existing order; a conflicting repeat is rejected.
 * Restaurant opening is checked at Acceptance, not here. A single document is
 * written, so no Unit of Work is needed.
 */
export class ReceiveOrderRequestHandler {
  readonly #accessControl: FulfillmentCoordinatorAccess;
  readonly #restaurants: RestaurantExistence;
  readonly #restaurantOrders: RestaurantOrderRepository;
  readonly #ids: IdGenerator;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: FulfillmentCoordinatorAccess;
    restaurants: RestaurantExistence;
    restaurantOrders: RestaurantOrderRepository;
    ids: IdGenerator;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#restaurants = dependencies.restaurants;
    this.#restaurantOrders = dependencies.restaurantOrders;
    this.#ids = dependencies.ids;
    this.#clock = dependencies.clock;
  }

  async execute(
    command: ReceiveOrderRequest,
  ): Promise<ReceiveOrderRequestResult> {
    await requirePermission(
      this.#accessControl.canCoordinateFulfillment(
        command.subject,
        command.restaurantId,
      ),
      "receive order request",
    );
    const existing = await this.#existing(command);
    if (existing !== undefined) {
      return existing;
    }
    if (!(await this.#restaurants.exists(command.restaurantId))) {
      throw new NotFoundError("Restaurant", command.restaurantId);
    }
    const order = RestaurantOrder.receive({
      id: this.#ids.next(),
      orderId: command.orderId,
      restaurantId: command.restaurantId,
      items: command.items,
      receivedAt: this.#clock.now(),
    });
    try {
      const version = await this.#restaurantOrders.save(order);
      order.pullEvents();
      return {
        restaurantOrder: toRestaurantOrderView(order, version),
        created: true,
      };
    } catch (error) {
      if (error instanceof DuplicateOrderIdError) {
        const concurrent = await this.#existing(command);
        if (concurrent !== undefined) {
          return concurrent;
        }
      }
      throw error;
    }
  }

  async #existing(
    command: ReceiveOrderRequest,
  ): Promise<ReceiveOrderRequestResult | undefined> {
    const existing = await this.#restaurantOrders.findByOrderId(
      command.orderId,
    );
    if (existing === undefined) {
      return undefined;
    }
    if (!existing.matchesRequest(command)) {
      throw new AlreadyExistsError("RestaurantOrder", command.orderId);
    }
    return { restaurantOrder: toRestaurantOrderView(existing), created: false };
  }
}
