import { requirePermission } from "../../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../../pkg/authz/subject.ts";
import type { UnitOfWork } from "../../../../../pkg/unit-of-work/unit-of-work.ts";
import { NotFoundError } from "../../../../../pkg/usecase/errors.ts";
import {
  AlreadyInStateError,
  type RestaurantOrderRepository,
} from "../../../domains/restaurantorder/restaurantorder.ts";
import type { FulfillmentProcessAccess } from "../../access-control/access-control.ts";
import {
  type RestaurantOrderView,
  toRestaurantOrderView,
} from "../../restaurant-order-view/restaurant-order-view.ts";

export type RecordPickup = Readonly<{
  /** `service` identity of the `orders` fulfillment process. */
  subject: Subject;
  /** Contract `Order ID` of `orders`; one Restaurant Order per Order. */
  orderId: string;
  pickedUpAt: Date;
}>;

/**
 * The courier took the order, as the workflow of `orders` reports: the kitchen
 * knows nothing about delivery. A `READY` Restaurant Order goes to `PICKED_UP`
 * and leaves the kitchen board. A repeat for a `PICKED_UP` order changes
 * nothing. The fact stays local: no Integration Event. The save carries the
 * kitchen Change Notification (ADR 36).
 */
export class RecordPickupHandler {
  readonly #accessControl: FulfillmentProcessAccess;
  readonly #unitOfWork: UnitOfWork;
  readonly #restaurantOrders: RestaurantOrderRepository;

  constructor(dependencies: {
    accessControl: FulfillmentProcessAccess;
    unitOfWork: UnitOfWork;
    restaurantOrders: RestaurantOrderRepository;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#unitOfWork = dependencies.unitOfWork;
    this.#restaurantOrders = dependencies.restaurantOrders;
  }

  async execute(command: RecordPickup): Promise<RestaurantOrderView> {
    await requirePermission(
      this.#accessControl.canCoordinateFulfillment(command.subject),
      "record pickup",
    );
    return this.#unitOfWork.run(async () => {
      const order = await this.#restaurantOrders.findByOrderId(command.orderId);
      if (order === undefined) {
        throw new NotFoundError("RestaurantOrder", command.orderId);
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
