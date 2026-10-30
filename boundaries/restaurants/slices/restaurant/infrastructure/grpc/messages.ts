import { create } from "@bufbuild/protobuf";
import { durationFromMs, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { RestaurantSchema } from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/restaurants_pb.js";
import type { StaffChangeResult } from "../../applications/change-staff/change-staff.ts";
import type {
  AcceptingCatalogRestaurantView,
  RestaurantView,
} from "../../applications/restaurant-view/restaurant-view.ts";

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

export const toStaffMessage = (result: StaffChangeResult) => ({
  restaurantId: result.restaurantId,
  staff: [...result.staff],
  changed: result.changed,
});
