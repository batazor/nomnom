import { NotFoundError } from "../../../../pkg/application/errors.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import type { KitchenAccessControl } from "../access-control.ts";
import {
  authorize,
  type RestaurantOrderView,
} from "../restaurant-order-support.ts";

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
  readonly #accessControl: KitchenAccessControl;
  readonly #reader: RestaurantOrderReader;

  constructor(dependencies: {
    accessControl: KitchenAccessControl;
    reader: RestaurantOrderReader;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#reader = dependencies.reader;
  }

  async execute(query: GetRestaurantOrder): Promise<RestaurantOrderView> {
    await authorize(
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
