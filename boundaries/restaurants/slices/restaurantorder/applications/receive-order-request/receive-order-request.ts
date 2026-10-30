import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { Clock } from "../../../../pkg/usecase/clock.ts";
import {
  AlreadyExistsError,
  NotFoundError,
} from "../../../../pkg/usecase/errors.ts";
import type { IdGenerator } from "../../../../pkg/usecase/id-generator.ts";
import type { MarketId } from "../../../restaurant/domains/restaurant/restaurant.ts";
import {
  DuplicateOrderIdError,
  type OrderScheduleInput,
  type PreparationItemInput,
  RestaurantOrder,
  type RestaurantOrderRepository,
} from "../../domains/restaurantorder/restaurantorder.ts";
import type { FulfillmentCoordinatorAccess } from "../access-control/access-control.ts";
import {
  type RestaurantOrderView,
  toRestaurantOrderView,
} from "../restaurant-order-view/restaurant-order-view.ts";

export type ReceiveOrderRequest = Readonly<{
  /** `service` identity of the `orders` fulfillment process. */
  subject: Subject;
  restaurantId: string;
  /** Contract `Order ID` from `orders`; one Restaurant Order per Order. */
  orderId: string;
  items: readonly PreparationItemInput[];
  /** Premium Priority chosen at Place Order (FR-651). */
  priority: string;
  /**
   * Delivery Window and Planned Ready At of a scheduled order (FR-683);
   * a partial block is invalid.
   */
  schedule?: OrderScheduleInput;
}>;

/** Market of a registered restaurant; `undefined` when it is not registered. */
export interface RestaurantMarkets {
  marketOf(restaurantId: string): Promise<MarketId | undefined>;
}

export type ReceiveOrderRequestResult = Readonly<{
  restaurantOrder: RestaurantOrderView;
  /** `false` when the request repeated an already received order. */
  created: boolean;
}>;

/**
 * Idempotently creates a `RECEIVED` Restaurant Order in the Market of the
 * restaurant that receives it (ADR 41). A repeat with the same
 * composition returns the existing order; a conflicting repeat is rejected.
 * Restaurant opening is checked at Acceptance, not here. A single document is
 * written, so no Unit of Work is needed.
 */
export class ReceiveOrderRequestHandler {
  readonly #accessControl: FulfillmentCoordinatorAccess;
  readonly #restaurants: RestaurantMarkets;
  readonly #restaurantOrders: RestaurantOrderRepository;
  readonly #ids: IdGenerator;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: FulfillmentCoordinatorAccess;
    restaurants: RestaurantMarkets;
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
    // The Restaurant Order takes the Market of its restaurant (ADR 41).
    const existing = await this.#existing(command);
    if (existing !== undefined) {
      return existing;
    }
    const marketId = await this.#restaurants.marketOf(command.restaurantId);
    if (marketId === undefined) {
      throw new NotFoundError("Restaurant", command.restaurantId);
    }
    const order = RestaurantOrder.receive({
      id: this.#ids.next(),
      orderId: command.orderId,
      restaurantId: command.restaurantId,
      marketId: marketId.value,
      items: command.items,
      priority: command.priority,
      ...(command.schedule === undefined ? {} : { schedule: command.schedule }),
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
