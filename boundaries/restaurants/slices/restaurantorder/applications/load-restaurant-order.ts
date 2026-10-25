import { NotFoundError } from "../../../pkg/application/errors.ts";
import type {
  RestaurantOrder,
  RestaurantOrderRepository,
} from "../domains/restaurantorder/restaurantorder.ts";

/**
 * Loads an order addressed by restaurant and id. An order of another
 * restaurant is reported as not found so its state is not disclosed.
 */
export const loadRestaurantOrder = async (
  orders: RestaurantOrderRepository,
  restaurantId: string,
  restaurantOrderId: string,
): Promise<RestaurantOrder> => {
  const order = await orders.get(restaurantOrderId);
  if (order === undefined || order.restaurantId !== restaurantId) {
    throw new NotFoundError("RestaurantOrder", restaurantOrderId);
  }
  return order;
};
