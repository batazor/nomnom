import type {
  OrderPriority,
  OrderScheduleValue,
  RestaurantOrder,
  RestaurantOrderSnapshot,
  RestaurantOrderState,
  RestaurantOrderTimeline,
} from "../domains/restaurantorder/restaurantorder.ts";

/** Representation of one Restaurant Order returned by commands and queries. */
export type RestaurantOrderView = Readonly<{
  restaurantOrderId: string;
  orderId: string;
  restaurantId: string;
  state: RestaurantOrderState;
  items: readonly Readonly<{
    menuItemId: string;
    quantity: number;
    options: readonly string[];
  }>[];
  timeline: RestaurantOrderTimeline;
  priority: OrderPriority;
  /** Delivery Window and Planned Ready At of a scheduled order (FR-683). */
  schedule: OrderScheduleValue | undefined;
  /** Cook assigned by Start Preparation, if it happened. */
  cookId: string | undefined;
  rejectionReason: string | undefined;
  failureReason: string | undefined;
  cancelledAtStage: RestaurantOrderState | undefined;
  version: number;
}>;

export const snapshotToView = (
  snapshot: RestaurantOrderSnapshot,
  version: number,
): RestaurantOrderView => ({
  restaurantOrderId: snapshot.id,
  orderId: snapshot.orderId,
  restaurantId: snapshot.restaurantId,
  state: snapshot.state,
  items: snapshot.items.map((item) => ({
    menuItemId: item.menuItemId,
    quantity: item.quantity,
    options: [...item.options],
  })),
  timeline: { ...snapshot.timeline },
  priority: snapshot.priority,
  schedule:
    snapshot.schedule === undefined ? undefined : { ...snapshot.schedule },
  cookId: snapshot.cookId,
  rejectionReason: snapshot.rejectionReason,
  failureReason: snapshot.failureReason,
  cancelledAtStage: snapshot.cancellation?.stage,
  version,
});

export const toRestaurantOrderView = (
  order: RestaurantOrder,
  version: number = order.version,
): RestaurantOrderView => snapshotToView(order.toSnapshot(), version);
