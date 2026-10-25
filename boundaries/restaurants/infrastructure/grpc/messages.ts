import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import type {
  MenuItemView,
  MenuView,
} from "../../slices/menu/applications/menu-support.ts";
import type { StaffChangeResult } from "../../slices/restaurant/applications/change-staff.ts";
import type { RestaurantView } from "../../slices/restaurant/applications/restaurant-view.ts";
import type { KitchenQueueEntry } from "../../slices/restaurantorder/applications/get-kitchen-queue/get-kitchen-queue.ts";
import type { ActiveRestaurantOrder } from "../../slices/restaurantorder/applications/list-active-restaurant-orders/list-active-restaurant-orders.ts";
import type { IncomingOrder } from "../../slices/restaurantorder/applications/list-incoming-orders/list-incoming-orders.ts";
import type { RestaurantOrderView } from "../../slices/restaurantorder/applications/restaurant-order-view.ts";
import {
  MenuItemSchema,
  RestaurantSchema,
} from "./gen/nomnom/restaurants/v1/restaurants_pb.js";
import {
  ActiveRestaurantOrderSchema,
  IncomingOrderSchema,
  KitchenQueueEntrySchema,
  RestaurantOrderSchema,
  RestaurantOrderState,
} from "./gen/nomnom/restaurants/v1/service_pb.js";

export const toRestaurantMessage = (view: RestaurantView) =>
  create(RestaurantSchema, {
    restaurantId: view.restaurantId,
    name: view.name,
    isOpen: view.isOpen,
    address: view.address,
  });

export const toMenuItemMessage = (item: MenuItemView) =>
  create(MenuItemSchema, {
    menuItemId: item.menuItemId,
    name: item.name,
    description: item.description ?? "",
    price: { amount: item.price.amount, currency: item.price.currency },
    available: item.isAvailable,
  });

export const toMenuItemMessages = (view: MenuView) =>
  view.items.map(toMenuItemMessage);

const optionalTimestamp = (value: Date | undefined) =>
  value === undefined ? undefined : timestampFromDate(value);

const STATE_MESSAGES: Readonly<
  Record<RestaurantOrderView["state"], RestaurantOrderState>
> = {
  RECEIVED: RestaurantOrderState.RECEIVED,
  ACCEPTED: RestaurantOrderState.ACCEPTED,
  QUEUED: RestaurantOrderState.QUEUED,
  PREPARING: RestaurantOrderState.PREPARING,
  ASSEMBLED: RestaurantOrderState.ASSEMBLED,
  READY: RestaurantOrderState.READY,
  REJECTED: RestaurantOrderState.REJECTED,
  CANCELLED: RestaurantOrderState.CANCELLED,
  FAILED: RestaurantOrderState.FAILED,
};

export const toRestaurantOrderMessage = (view: RestaurantOrderView) =>
  create(RestaurantOrderSchema, {
    restaurantOrderId: view.restaurantOrderId,
    orderId: view.orderId,
    restaurantId: view.restaurantId,
    state: STATE_MESSAGES[view.state],
    expectedReadyAt: optionalTimestamp(view.timeline.expectedReadyAt),
    version: BigInt(view.version),
  });

export const toKitchenQueueEntryMessage = (
  restaurantId: string,
  entry: KitchenQueueEntry,
) =>
  create(KitchenQueueEntrySchema, {
    restaurantOrder: {
      restaurantOrderId: entry.restaurantOrderId,
      orderId: entry.orderId,
      restaurantId,
      state: STATE_MESSAGES[entry.state],
      expectedReadyAt: optionalTimestamp(entry.expectedReadyAt),
      version: BigInt(entry.version),
    },
    acceptedAt: optionalTimestamp(entry.acceptedAt),
    queuedAt: optionalTimestamp(entry.queuedAt),
  });

export const toIncomingOrderMessage = (
  restaurantId: string,
  order: IncomingOrder,
) =>
  create(IncomingOrderSchema, {
    restaurantOrder: {
      restaurantOrderId: order.restaurantOrderId,
      orderId: order.orderId,
      restaurantId,
      state: RestaurantOrderState.RECEIVED,
      version: BigInt(order.version),
    },
    items: order.items.map((item) => ({
      menuItemId: item.menuItemId,
      quantity: item.quantity,
      options: [...item.options],
    })),
    receivedAt: optionalTimestamp(order.receivedAt),
  });

export const toActiveRestaurantOrderMessage = (
  restaurantId: string,
  order: ActiveRestaurantOrder,
) =>
  create(ActiveRestaurantOrderSchema, {
    restaurantOrder: {
      restaurantOrderId: order.restaurantOrderId,
      orderId: order.orderId,
      restaurantId,
      state: STATE_MESSAGES[order.state],
      expectedReadyAt: optionalTimestamp(order.expectedReadyAt),
      version: BigInt(order.version),
    },
    items: order.items.map((item) => ({
      menuItemId: item.menuItemId,
      quantity: item.quantity,
      options: [...item.options],
    })),
    receivedAt: optionalTimestamp(order.receivedAt),
  });

export const toStaffMessage = (result: StaffChangeResult) => ({
  restaurantId: result.restaurantId,
  staff: [...result.staff],
  changed: result.changed,
});
