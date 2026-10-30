import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import { NotFoundError } from "../../../../pkg/usecase/errors.ts";
import type { RestaurantOrderViewAccess } from "../access-control.ts";
import type { RestaurantOrderView } from "../restaurant-order-view.ts";

export type GetRestaurantOrder = Readonly<{
  subject: Subject;
  restaurantId: string;
  restaurantOrderId: string;
}>;

/** Read model port of single Restaurant Orders of a restaurant. */
export interface RestaurantOrderReader {
  get(
    restaurantId: string,
    restaurantOrderId: string,
  ): Promise<RestaurantOrderView | undefined>;
}

/** Returns composition, State, timeline and stored reasons of one order. */
export class GetRestaurantOrderHandler {
  readonly #accessControl: RestaurantOrderViewAccess;
  readonly #reader: RestaurantOrderReader;

  constructor(dependencies: {
    accessControl: RestaurantOrderViewAccess;
    reader: RestaurantOrderReader;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#reader = dependencies.reader;
  }

  async execute(query: GetRestaurantOrder): Promise<RestaurantOrderView> {
    await requirePermission(
      this.#accessControl.canViewRestaurantOrders(
        query.subject,
        query.restaurantId,
      ),
      "view restaurant order",
    );
    const order = await this.#reader.get(
      query.restaurantId,
      query.restaurantOrderId,
    );
    if (order === undefined) {
      throw new NotFoundError("RestaurantOrder", query.restaurantOrderId);
    }
    return order;
  }
}
