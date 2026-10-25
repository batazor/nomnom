import type {
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
  rejectionReason: snapshot.rejectionReason,
  failureReason: snapshot.failureReason,
  cancelledAtStage: snapshot.cancellation?.stage,
  version,
});

export const toRestaurantOrderView = (
  order: RestaurantOrder,
  version: number = order.version,
): RestaurantOrderView => snapshotToView(order.toSnapshot(), version);
