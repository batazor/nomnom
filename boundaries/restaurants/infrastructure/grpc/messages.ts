import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";
import type {
  MenuItemView,
  MenuView,
} from "../../slices/menu/applications/menu-support.ts";
import type { StaffChangeResult } from "../../slices/restaurant/applications/change-staff.ts";
import type {
  CatalogRestaurantView,
  RestaurantView,
} from "../../slices/restaurant/applications/restaurant-view.ts";
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
  OrderPriority as OrderPriorityMessage,
  RestaurantOrderSchema,
  RestaurantOrderState,
} from "./gen/nomnom/restaurants/v1/service_pb.js";

export const toRestaurantMessage = (view: RestaurantView) =>
  create(RestaurantSchema, {
    restaurantId: view.restaurantId,
    name: view.name,
    isOpen: view.isOpen,
    marketId: view.marketId,
    ...(view.premiumFee === undefined
      ? {}
      : { premiumFee: { ...view.premiumFee } }),
  });

/** Get Restaurant and List Restaurants also carry the wait (FR-204). */
export const toCatalogRestaurantMessage = (view: CatalogRestaurantView) =>
  create(RestaurantSchema, {
    restaurantId: view.restaurantId,
    name: view.name,
    isOpen: view.isOpen,
    marketId: view.marketId,
    ...(view.premiumFee === undefined
      ? {}
      : { premiumFee: { ...view.premiumFee } }),
    waitTime: {
      elevated: view.waitTime.elevated,
      expectedWaitMinutes: view.waitTime.expectedWaitMinutes,
    },
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
  PICKED_UP: RestaurantOrderState.PICKED_UP,
  REJECTED: RestaurantOrderState.REJECTED,
  CANCELLED: RestaurantOrderState.CANCELLED,
  FAILED: RestaurantOrderState.FAILED,
};

const PRIORITY_MESSAGES: Readonly<
  Record<RestaurantOrderView["priority"], OrderPriorityMessage>
> = {
  STANDARD: OrderPriorityMessage.STANDARD,
  PREMIUM: OrderPriorityMessage.PREMIUM,
};

/** Required in ReceiveOrderRequest (FR-651); UNSPECIFIED is invalid input. */
export const fromPriorityMessage = (
  priority: OrderPriorityMessage,
): RestaurantOrderView["priority"] => {
  switch (priority) {
    case OrderPriorityMessage.STANDARD:
      return "STANDARD";
    case OrderPriorityMessage.PREMIUM:
      return "PREMIUM";
    default:
      throw new ConnectError(
        `priority ${priority} is not STANDARD or PREMIUM`,
        Code.InvalidArgument,
      );
  }
};

export const toRestaurantOrderMessage = (view: RestaurantOrderView) =>
  create(RestaurantOrderSchema, {
    restaurantOrderId: view.restaurantOrderId,
    orderId: view.orderId,
    restaurantId: view.restaurantId,
    state: STATE_MESSAGES[view.state],
    expectedReadyAt: optionalTimestamp(view.timeline.expectedReadyAt),
    version: BigInt(view.version),
    cookId: view.cookId ?? "",
    priority: PRIORITY_MESSAGES[view.priority],
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
      priority: PRIORITY_MESSAGES[entry.priority],
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
      cookId: order.cookId ?? "",
      priority: PRIORITY_MESSAGES[order.priority],
    },
    items: order.items.map((item) => ({
      menuItemId: item.menuItemId,
      quantity: item.quantity,
      options: [...item.options],
    })),
    receivedAt: optionalTimestamp(order.receivedAt),
    acceptedAt: optionalTimestamp(order.acceptedAt),
  });

export const toStaffMessage = (result: StaffChangeResult) => ({
  restaurantId: result.restaurantId,
  staff: [...result.staff],
  changed: result.changed,
});
