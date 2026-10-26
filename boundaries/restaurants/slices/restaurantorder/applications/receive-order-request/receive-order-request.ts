import type { Clock } from "../../../../pkg/application/clock.ts";
import {
  AlreadyExistsError,
  NotFoundError,
} from "../../../../pkg/application/errors.ts";
import type { IdGenerator } from "../../../../pkg/application/id-generator.ts";
import { requirePermission } from "../../../../pkg/application/require-permission.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import { MarketId } from "../../../restaurant/domains/restaurant/restaurant.ts";
import {
  DuplicateOrderIdError,
  MarketMismatchError,
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
  /**
   * Market the caller expects the order in (`OrderPlaced.market_id`), when it
   * names one. The Restaurant Order always takes the Market of its
   * restaurant (ADR 41); a different named Market is a
   * {@link MarketMismatchError}, never resolved by picking one of them.
   */
  marketId?: string;
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
    const requestedMarket =
      command.marketId === undefined
        ? undefined
        : MarketId.of(command.marketId);
    const existing = await this.#existing(command, requestedMarket);
    if (existing !== undefined) {
      return existing;
    }
    const marketId = await this.#restaurants.marketOf(command.restaurantId);
    if (marketId === undefined) {
      throw new NotFoundError("Restaurant", command.restaurantId);
    }
    requireSameMarket(command.orderId, marketId, requestedMarket);
    const order = RestaurantOrder.receive({
      id: this.#ids.next(),
      orderId: command.orderId,
      restaurantId: command.restaurantId,
      marketId: marketId.value,
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
        const concurrent = await this.#existing(command, requestedMarket);
        if (concurrent !== undefined) {
          return concurrent;
        }
      }
      throw error;
    }
  }

  async #existing(
    command: ReceiveOrderRequest,
    requestedMarket: MarketId | undefined,
  ): Promise<ReceiveOrderRequestResult | undefined> {
    const existing = await this.#restaurantOrders.findByOrderId(
      command.orderId,
    );
    if (existing === undefined) {
      return undefined;
    }
    requireSameMarket(command.orderId, existing.marketId, requestedMarket);
    if (!existing.matchesRequest(command)) {
      throw new AlreadyExistsError("RestaurantOrder", command.orderId);
    }
    return { restaurantOrder: toRestaurantOrderView(existing), created: false };
  }
}

/** A named Market must be the Market of the restaurant or received order. */
const requireSameMarket = (
  orderId: string,
  marketId: MarketId,
  requested: MarketId | undefined,
): void => {
  if (requested !== undefined && !requested.equals(marketId)) {
    throw new MarketMismatchError({
      orderId,
      expectedMarketId: marketId.value,
      requestedMarketId: requested.value,
    });
  }
};
