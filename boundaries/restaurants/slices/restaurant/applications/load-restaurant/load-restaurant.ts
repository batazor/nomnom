import { NotFoundError } from "../../../../pkg/usecase/errors.ts";
import type {
  Restaurant,
  RestaurantRepository,
} from "../../domains/restaurant/restaurant.ts";

export const loadRestaurant = async (
  restaurants: RestaurantRepository,
  restaurantId: string,
): Promise<Restaurant> => {
  const restaurant = await restaurants.get(restaurantId);
  if (restaurant === undefined) {
    throw new NotFoundError("Restaurant", restaurantId);
  }
  return restaurant;
};
