import { create } from "@bufbuild/protobuf";
import { durationFromMs, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";
import type {
  MenuItemView,
  MenuView,
} from "../../slices/menu/applications/menu-support/menu-support.ts";
import type { StaffChangeResult } from "../../slices/restaurant/applications/change-staff/change-staff.ts";
import type {
  AcceptingCatalogRestaurantView,
  RestaurantView,
} from "../../slices/restaurant/applications/restaurant-view/restaurant-view.ts";
import type { KitchenQueueEntry } from "../../slices/restaurantorder/applications/get-kitchen-queue/get-kitchen-queue.ts";
import type { ActiveRestaurantOrder } from "../../slices/restaurantorder/applications/list-active-restaurant-orders/list-active-restaurant-orders.ts";
import type { IncomingOrder } from "../../slices/restaurantorder/applications/list-incoming-orders/list-incoming-orders.ts";
import type { ScheduledOrder } from "../../slices/restaurantorder/applications/list-scheduled-orders/list-scheduled-orders.ts";
import type { RestaurantOrderView } from "../../slices/restaurantorder/applications/restaurant-order-view/restaurant-order-view.ts";
import type { OrderScheduleValue } from "../../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import type { DeliveryWindow } from "../../slices/scheduleslot/domains/scheduleslot/scheduleslot.ts";
import {
  DeliveryWindowSchema,
  MenuItemSchema,
  RestaurantSchema,
} from "./gen/nomnom/restaurants/v1/restaurants_pb.js";
import {
  ActiveRestaurantOrderSchema,
  IncomingOrderSchema,
  KitchenQueueEntrySchema,
  OrderPriority as OrderPriorityMessage,
  OrderScheduleSchema,
  RestaurantOrderSchema,
  RestaurantOrderState,
  ScheduledRestaurantOrderSchema,
} from "./gen/nomnom/restaurants/v1/service_pb.js";

const restaurantFields = (view: RestaurantView) => ({
  restaurantId: view.restaurantId,
  name: view.name,
  isOpen: view.isOpen,
  marketId: view.marketId,
  timeZone: view.timeZone,
  openingHours: view.openingHours.map((interval) => ({
    dayOfWeek: interval.day,
    opens: interval.opens,
    closes: interval.closes,
  })),
  ...(view.premiumFee === undefined
    ? {}
    : { premiumFee: { ...view.premiumFee } }),
  ...(view.scheduledOrders === undefined
    ? {}
    : {
        scheduledOrders: {
          slotCapacity: view.scheduledOrders.slotCapacity,
          openingLead: durationFromMs(view.scheduledOrders.openingLeadMs),
          closingGrace: durationFromMs(view.scheduledOrders.closingGraceMs),
        },
      }),
});

export const toRestaurantMessage = (view: RestaurantView) =>
  create(RestaurantSchema, restaurantFields(view));

/**
 * Get Restaurant, Batch Get Restaurants and List Restaurants also carry the
 * wait and the acceptance of orders at the time of the read (FR-204).
 */
export const toCatalogRestaurantMessage = (
  view: AcceptingCatalogRestaurantView,
) =>
  create(RestaurantSchema, {
    ...restaurantFields(view),
    acceptingOrders: view.acceptingOrders,
    ...(view.nextOpeningAt === undefined
      ? {}
      : { nextOpeningAt: timestampFromDate(view.nextOpeningAt) }),
    waitTime: {
      elevated: view.waitTime.elevated,
      expectedWaitMinutes: view.waitTime.expectedWaitMinutes,
    },
    baselinePreparation: durationFromMs(view.waitTime.baselinePreparationMs),
  });

/** Delivery Window `[start, end)` of a scheduled order. */
export const toDeliveryWindowMessage = (window: DeliveryWindow) =>
  create(DeliveryWindowSchema, {
    start: timestampFromDate(window.start),
    end: timestampFromDate(window.end),
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

const toScheduleMessage = (schedule: OrderScheduleValue) =>
  create(OrderScheduleSchema, {
    windowStart: timestampFromDate(schedule.windowStart),
    windowEnd: timestampFromDate(schedule.windowEnd),
    plannedReadyAt: timestampFromDate(schedule.plannedReadyAt),
  });

const optionalSchedule = (schedule: OrderScheduleValue | undefined) =>
  schedule === undefined ? undefined : toScheduleMessage(schedule);

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
    schedule: optionalSchedule(view.schedule),
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
      schedule: optionalSchedule(entry.schedule),
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
      schedule: optionalSchedule(order.schedule),
    },
    items: order.items.map((item) => ({
      menuItemId: item.menuItemId,
      quantity: item.quantity,
      options: [...item.options],
    })),
    receivedAt: optionalTimestamp(order.receivedAt),
    acceptedAt: optionalTimestamp(order.acceptedAt),
  });

export const toScheduledOrderMessage = (order: ScheduledOrder) =>
  create(ScheduledRestaurantOrderSchema, {
    restaurantOrderId: order.restaurantOrderId,
    orderId: order.orderId,
    items: order.items.map((item) => ({
      menuItemId: item.menuItemId,
      quantity: item.quantity,
      options: [...item.options],
    })),
    schedule: toScheduleMessage(order.schedule),
    priority: PRIORITY_MESSAGES[order.priority],
    version: BigInt(order.version),
  });

export const toStaffMessage = (result: StaffChangeResult) => ({
  restaurantId: result.restaurantId,
  staff: [...result.staff],
  changed: result.changed,
});
