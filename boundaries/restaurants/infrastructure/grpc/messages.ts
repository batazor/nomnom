import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { StaffChangeResult } from "../../slices/restaurant/applications/change-staff.ts";
import type { RestaurantView } from "../../slices/restaurant/applications/restaurant-view.ts";
import type { RestaurantOrderView } from "../../slices/restaurantorder/applications/restaurant-order-support.ts";
import { RestaurantSchema } from "./gen/nomnom/restaurants/v1/restaurants_pb.js";
import {
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
    expectedReadyAt:
      view.timeline.expectedReadyAt === undefined
        ? undefined
        : timestampFromDate(view.timeline.expectedReadyAt),
    version: BigInt(view.version),
  });

export const toStaffMessage = (result: StaffChangeResult) => ({
  restaurantId: result.restaurantId,
  staff: [...result.staff],
  changed: result.changed,
});
