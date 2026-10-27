import { NotFoundError } from "../../../../pkg/application/errors.ts";
import { requirePermission } from "../../../../pkg/application/require-permission.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import type { UnitOfWork } from "../../../../pkg/application/unit-of-work.ts";
import { MarketId } from "../../../restaurant/domains/restaurant/restaurant.ts";
import {
  AlreadyInStateError,
  MarketMismatchError,
  type RestaurantOrderRepository,
} from "../../domains/restaurantorder/restaurantorder.ts";
import type { PickupWitnessAccess } from "../access-control.ts";
import {
  type RestaurantOrderView,
  toRestaurantOrderView,
} from "../restaurant-order-view.ts";

export type RecordPickup = Readonly<{
  /** `service` identity of the `delivery` event stream. */
  subject: Subject;
  /** Contract `Order ID` of `orders`; one Restaurant Order per Order. */
  orderId: string;
  /** Market of the Delivery (`DeliveryPickedUp.market_id`), when named. */
  marketId?: string;
  pickedUpAt: Date;
}>;

/**
 * The courier took the order (Pickup of `delivery`): a `READY` Restaurant
 * Order goes to `PICKED_UP` and leaves the kitchen board. A repeat for a
 * `PICKED_UP` order changes nothing. The fact stays local: no Integration
 * Event. The save carries the kitchen Change Notification (ADR 36).
 */
export class RecordPickupHandler {
  readonly #accessControl: PickupWitnessAccess;
  readonly #unitOfWork: UnitOfWork;
  readonly #restaurantOrders: RestaurantOrderRepository;

  constructor(dependencies: {
    accessControl: PickupWitnessAccess;
    unitOfWork: UnitOfWork;
    restaurantOrders: RestaurantOrderRepository;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#unitOfWork = dependencies.unitOfWork;
    this.#restaurantOrders = dependencies.restaurantOrders;
  }

  async execute(command: RecordPickup): Promise<RestaurantOrderView> {
    await requirePermission(
      this.#accessControl.canRecordPickup(command.subject),
      "record pickup",
    );
    return this.#unitOfWork.run(async () => {
      const order = await this.#restaurantOrders.findByOrderId(command.orderId);
      if (order === undefined) {
        throw new NotFoundError("RestaurantOrder", command.orderId);
      }
      // A Delivery is in the Market of its order (ADR 41); another Market
      // names a different order, and neither is picked silently.
      if (
        command.marketId !== undefined &&
        !MarketId.of(command.marketId).equals(order.marketId)
      ) {
        throw new MarketMismatchError({
          orderId: command.orderId,
          expectedMarketId: order.marketId.value,
          requestedMarketId: command.marketId,
        });
      }
      try {
        order.recordPickup({ pickedUpAt: command.pickedUpAt });
      } catch (error) {
        if (error instanceof AlreadyInStateError) {
          return toRestaurantOrderView(order);
        }
        throw error;
      }
      const version = await this.#restaurantOrders.save(order);
      order.pullEvents();
      return toRestaurantOrderView(order, version);
    });
  }
}
