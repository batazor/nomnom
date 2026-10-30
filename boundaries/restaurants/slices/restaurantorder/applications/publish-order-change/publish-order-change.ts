import type { Outbox } from "../../../../pkg/outbox/outbox.ts";
import { publishAggregateChange } from "../../../../pkg/outbox/publish-aggregate-change.ts";
import type {
  RestaurantOrder,
  RestaurantOrderEventType,
  RestaurantOrderRepository,
} from "../../domains/restaurantorder/restaurantorder.ts";

/**
 * Saves the order and passes only the facts of `published` to the Outbox;
 * local facts are discarded. Must run inside the active Unit of Work.
 */
export const saveAndPublish = (
  orders: RestaurantOrderRepository,
  outbox: Outbox,
  order: RestaurantOrder,
  published: readonly RestaurantOrderEventType[],
): Promise<number> =>
  publishAggregateChange(orders, outbox, "RestaurantOrder", published, order);
