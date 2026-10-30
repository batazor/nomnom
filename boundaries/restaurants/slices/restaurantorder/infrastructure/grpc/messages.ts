import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  ActiveRestaurantOrderSchema,
  IncomingOrderSchema,
  KitchenQueueEntrySchema,
  OrderPriority as OrderPriorityMessage,
  OrderScheduleSchema,
  RestaurantOrderSchema,
  RestaurantOrderState,
  ScheduledRestaurantOrderSchema,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import type { KitchenQueueEntry } from "../../applications/board/get-kitchen-queue/get-kitchen-queue.ts";
import type { ActiveRestaurantOrder } from "../../applications/board/list-active-restaurant-orders/list-active-restaurant-orders.ts";
import type { IncomingOrder } from "../../applications/board/list-incoming-orders/list-incoming-orders.ts";
import type { ScheduledOrder } from "../../applications/board/list-scheduled-orders/list-scheduled-orders.ts";
import type { RestaurantOrderView } from "../../applications/restaurant-order-view/restaurant-order-view.ts";
import type { OrderScheduleValue } from "../../domains/restaurantorder/restaurantorder.ts";

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
