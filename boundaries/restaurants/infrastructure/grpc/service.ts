import { create } from "@bufbuild/protobuf";
import { type Timestamp, timestampDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError, type ServiceImpl } from "@connectrpc/connect";
import type { GetCookPoolHandler } from "../../slices/cook/applications/get-cook-pool/get-cook-pool.ts";
import type { HireCookHandler } from "../../slices/cook/applications/hire-cook/hire-cook.ts";
import type { PutCookOnLineHandler } from "../../slices/cook/applications/put-cook-on-line/put-cook-on-line.ts";
import type { RetireCookHandler } from "../../slices/cook/applications/retire-cook/retire-cook.ts";
import type { TakeCookOffLineHandler } from "../../slices/cook/applications/take-cook-off-line/take-cook-off-line.ts";
import type { AddMenuItemHandler } from "../../slices/menu/applications/add-menu-item/add-menu-item.ts";
import type { BatchGetMenusHandler } from "../../slices/menu/applications/batch-get-menus/batch-get-menus.ts";
import type { CreateMenuHandler } from "../../slices/menu/applications/create-menu/create-menu.ts";
import type { GetMenuHandler } from "../../slices/menu/applications/get-menu/get-menu.ts";
import type { SetMenuItemAvailabilityHandler } from "../../slices/menu/applications/set-menu-item-availability/set-menu-item-availability.ts";
import type { UpdateMenuItemHandler } from "../../slices/menu/applications/update-menu-item/update-menu-item.ts";
import type { AssignStaffHandler } from "../../slices/restaurant/applications/assign-staff/assign-staff.ts";
import type { BatchGetRestaurantsHandler } from "../../slices/restaurant/applications/batch-get-restaurants/batch-get-restaurants.ts";
import type { CloseRestaurantHandler } from "../../slices/restaurant/applications/close-restaurant/close-restaurant.ts";
import type { GetRestaurantHandler } from "../../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import type { ListOwnedRestaurantsHandler } from "../../slices/restaurant/applications/list-owned-restaurants/list-owned-restaurants.ts";
import type { ListRestaurantsHandler } from "../../slices/restaurant/applications/list-restaurants/list-restaurants.ts";
import type { OpenRestaurantHandler } from "../../slices/restaurant/applications/open-restaurant/open-restaurant.ts";
import type { RegisterRestaurantHandler } from "../../slices/restaurant/applications/register-restaurant/register-restaurant.ts";
import type { RevokeStaffHandler } from "../../slices/restaurant/applications/revoke-staff/revoke-staff.ts";
import type { SetOpeningHoursHandler } from "../../slices/restaurant/applications/set-opening-hours/set-opening-hours.ts";
import type { SetScheduledOrderSettingsHandler } from "../../slices/restaurant/applications/set-scheduled-order-settings/set-scheduled-order-settings.ts";
import type { UpdateRestaurantHandler } from "../../slices/restaurant/applications/update-restaurant/update-restaurant.ts";
import type { AcceptOrderHandler } from "../../slices/restaurantorder/applications/accept-order/accept-order.ts";
import type { CancelRestaurantOrderHandler } from "../../slices/restaurantorder/applications/cancel-restaurant-order/cancel-restaurant-order.ts";
import type { GetKitchenQueueHandler } from "../../slices/restaurantorder/applications/get-kitchen-queue/get-kitchen-queue.ts";
import type { ListActiveRestaurantOrdersHandler } from "../../slices/restaurantorder/applications/list-active-restaurant-orders/list-active-restaurant-orders.ts";
import type { ListIncomingOrdersHandler } from "../../slices/restaurantorder/applications/list-incoming-orders/list-incoming-orders.ts";
import type { ListScheduledOrdersHandler } from "../../slices/restaurantorder/applications/list-scheduled-orders/list-scheduled-orders.ts";
import type { MarkAssembledHandler } from "../../slices/restaurantorder/applications/mark-assembled/mark-assembled.ts";
import type { MarkReadyForPickupHandler } from "../../slices/restaurantorder/applications/mark-ready-for-pickup/mark-ready-for-pickup.ts";
import type { QueueRestaurantOrderHandler } from "../../slices/restaurantorder/applications/queue-restaurant-order/queue-restaurant-order.ts";
import type { ReceiveOrderRequestHandler } from "../../slices/restaurantorder/applications/receive-order-request/receive-order-request.ts";
import type { RecordPickupHandler } from "../../slices/restaurantorder/applications/record-pickup/record-pickup.ts";
import type { RejectOrderHandler } from "../../slices/restaurantorder/applications/reject-order/reject-order.ts";
import type { ReportPreparationDelayHandler } from "../../slices/restaurantorder/applications/report-preparation-delay/report-preparation-delay.ts";
import type { ReportPreparationFailureHandler } from "../../slices/restaurantorder/applications/report-preparation-failure/report-preparation-failure.ts";
import type { StartPreparationHandler } from "../../slices/restaurantorder/applications/start-preparation/start-preparation.ts";
import type { ConfirmScheduledSlotHandler } from "../../slices/scheduleslot/applications/confirm-scheduled-slot/confirm-scheduled-slot.ts";
import type { ListScheduledSlotsHandler } from "../../slices/scheduleslot/applications/list-scheduled-slots/list-scheduled-slots.ts";
import type { ReleaseScheduledSlotHandler } from "../../slices/scheduleslot/applications/release-scheduled-slot/release-scheduled-slot.ts";
import type { ReserveScheduledSlotHandler } from "../../slices/scheduleslot/applications/reserve-scheduled-slot/reserve-scheduled-slot.ts";
import type { GetSimulatorHandler } from "../../slices/simulator/applications/get-simulator/get-simulator.ts";
import type { SetSimulatorEnabledHandler } from "../../slices/simulator/applications/set-simulator-enabled/set-simulator-enabled.ts";
import { createCookPoolRpcs } from "./cook-pool.ts";
import { requireField, transportError } from "./errors.ts";
import {
  AssignStaffResponseSchema,
  BatchGetMenusResponseSchema,
  BatchGetRestaurantsResponseSchema,
  CancelRestaurantOrderResponseSchema,
  GetMenuResponseSchema,
  GetRestaurantResponseSchema,
  ListOwnedRestaurantsResponseSchema,
  ListRestaurantsResponseSchema,
  QueueRestaurantOrderResponseSchema,
  ReceiveOrderRequestResponseSchema,
  RecordRestaurantOrderPickupResponseSchema,
  RegisterRestaurantResponseSchema,
  type RestaurantsService,
  RevokeStaffResponseSchema,
} from "./gen/nomnom/restaurants/v1/service_pb.js";
import { createKitchenRpcs } from "./kitchen.ts";
import { createKitchenBoardRpcs } from "./kitchen-board.ts";
import { createMenuRpcs } from "./menu-commands.ts";
import {
  fromPriorityMessage,
  toCatalogRestaurantMessage,
  toMenuItemMessages,
  toRestaurantMessage,
  toRestaurantOrderMessage,
  toStaffMessage,
} from "./messages.ts";
import { createOpeningRpcs } from "./restaurant-commands.ts";
import { withRequestRevision } from "./revision.ts";
import { createScheduleSlotRpcs } from "./schedule-slots.ts";
import { createSimulatorRpcs } from "./simulator.ts";
import type { SubjectResolver } from "./subject.ts";

export {
  AUTHORIZATION_REVISION_HEADER,
  withRequestRevision,
} from "./revision.ts";

const optionalDate = (value: Timestamp | undefined): Date | undefined =>
  value === undefined ? undefined : timestampDate(value);

export type RestaurantsServiceDependencies = Readonly<{
  getRestaurant: Pick<GetRestaurantHandler, "execute">;
  getMenu: Pick<GetMenuHandler, "execute">;
  batchGetRestaurants: Pick<BatchGetRestaurantsHandler, "execute">;
  batchGetMenus: Pick<BatchGetMenusHandler, "execute">;
  listRestaurants: Pick<ListRestaurantsHandler, "execute">;
  listOwnedRestaurants: Pick<ListOwnedRestaurantsHandler, "execute">;
  receiveOrderRequest: Pick<ReceiveOrderRequestHandler, "execute">;
  cancelRestaurantOrder: Pick<CancelRestaurantOrderHandler, "execute">;
  queueRestaurantOrder: Pick<QueueRestaurantOrderHandler, "execute">;
  recordPickup: Pick<RecordPickupHandler, "execute">;
  listScheduledSlots: Pick<ListScheduledSlotsHandler, "execute">;
  reserveScheduledSlot: Pick<ReserveScheduledSlotHandler, "execute">;
  confirmScheduledSlot: Pick<ConfirmScheduledSlotHandler, "execute">;
  releaseScheduledSlot: Pick<ReleaseScheduledSlotHandler, "execute">;
  acceptRestaurantOrder: Pick<AcceptOrderHandler, "execute">;
  rejectRestaurantOrder: Pick<RejectOrderHandler, "execute">;
  startPreparation: Pick<StartPreparationHandler, "execute">;
  markRestaurantOrderAssembled: Pick<MarkAssembledHandler, "execute">;
  markRestaurantOrderReady: Pick<MarkReadyForPickupHandler, "execute">;
  reportPreparationDelay: Pick<ReportPreparationDelayHandler, "execute">;
  reportPreparationFailure: Pick<ReportPreparationFailureHandler, "execute">;
  getKitchenQueue: Pick<GetKitchenQueueHandler, "execute">;
  listIncomingOrders: Pick<ListIncomingOrdersHandler, "execute">;
  listScheduledOrders: Pick<ListScheduledOrdersHandler, "execute">;
  listActiveRestaurantOrders: Pick<
    ListActiveRestaurantOrdersHandler,
    "execute"
  >;
  registerRestaurant: Pick<RegisterRestaurantHandler, "execute">;
  updateRestaurant: Pick<UpdateRestaurantHandler, "execute">;
  setOpeningHours: Pick<SetOpeningHoursHandler, "execute">;
  setScheduledOrderSettings: Pick<SetScheduledOrderSettingsHandler, "execute">;
  assignStaff: Pick<AssignStaffHandler, "execute">;
  revokeStaff: Pick<RevokeStaffHandler, "execute">;
  openRestaurant: Pick<OpenRestaurantHandler, "execute">;
  closeRestaurant: Pick<CloseRestaurantHandler, "execute">;
  createMenu: Pick<CreateMenuHandler, "execute">;
  addMenuItem: Pick<AddMenuItemHandler, "execute">;
  updateMenuItem: Pick<UpdateMenuItemHandler, "execute">;
  setMenuItemAvailability: Pick<SetMenuItemAvailabilityHandler, "execute">;
  hireCook: Pick<HireCookHandler, "execute">;
  putCookOnLine: Pick<PutCookOnLineHandler, "execute">;
  takeCookOffLine: Pick<TakeCookOffLineHandler, "execute">;
  retireCook: Pick<RetireCookHandler, "execute">;
  getCookPool: Pick<GetCookPoolHandler, "execute">;
  getSimulator: Pick<GetSimulatorHandler, "execute">;
  setSimulatorEnabled: Pick<SetSimulatorEnabledHandler, "execute">;
  subjects: SubjectResolver;
}>;

/** Connect/gRPC adapter. Generated messages never cross this boundary. */
export const createRestaurantsService = (
  dependencies: RestaurantsServiceDependencies,
): ServiceImpl<typeof RestaurantsService> => {
  const subjects = dependencies.subjects;
  return {
    async getRestaurant(request) {
      try {
        const view = await dependencies.getRestaurant.execute({
          restaurantId: request.restaurantId,
        });
        return create(GetRestaurantResponseSchema, {
          restaurant: toCatalogRestaurantMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async getMenu(request) {
      try {
        requireField("restaurant_id", request.restaurantId);
        const view = await dependencies.getMenu.execute({
          restaurantId: request.restaurantId,
        });
        return create(GetMenuResponseSchema, {
          items: toMenuItemMessages(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async batchGetRestaurants(request) {
      try {
        const views = await dependencies.batchGetRestaurants.execute({
          restaurantIds: request.restaurantIds,
        });
        return create(BatchGetRestaurantsResponseSchema, {
          restaurants: views.map(toCatalogRestaurantMessage),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async batchGetMenus(request) {
      try {
        const views = await dependencies.batchGetMenus.execute({
          restaurantIds: request.restaurantIds,
        });
        return create(BatchGetMenusResponseSchema, {
          menus: views.map((view) => ({
            restaurantId: view.restaurantId,
            items: toMenuItemMessages(view),
          })),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async listRestaurants(request) {
      try {
        // Market is required (ADR 41). Zero values of proto3 mean "not set":
        // default page size, first page.
        requireField("market_id", request.marketId);
        const result = await dependencies.listRestaurants.execute({
          marketId: request.marketId,
          includeClosed: request.includeClosed,
          limit: request.pageSize === 0 ? undefined : request.pageSize,
          cursor: request.pageToken === "" ? undefined : request.pageToken,
        });
        return create(ListRestaurantsResponseSchema, {
          restaurants: result.restaurants.map(toCatalogRestaurantMessage),
          nextPageToken: result.nextCursor ?? "",
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async listOwnedRestaurants(request, context) {
      try {
        requireField("market_id", request.marketId);
        const result = await dependencies.listOwnedRestaurants.execute({
          subject: await subjects.resolve(context),
          marketId: request.marketId,
          limit: request.pageSize === 0 ? undefined : request.pageSize,
          cursor: request.pageToken === "" ? undefined : request.pageToken,
        });
        return create(ListOwnedRestaurantsResponseSchema, {
          restaurants: result.restaurants.map(toRestaurantMessage),
          nextPageToken: result.nextCursor ?? "",
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async receiveOrderRequest(request, context) {
      try {
        const subject = withRequestRevision(
          await subjects.resolve(context),
          context,
        );
        requireField("restaurant_id", request.restaurantId);
        requireField("order_id", request.orderId);
        const result = await dependencies.receiveOrderRequest.execute({
          subject,
          restaurantId: request.restaurantId,
          orderId: request.orderId,
          items: request.items.map((item) => ({
            menuItemId: item.menuItemId,
            quantity: item.quantity,
          })),
          priority: fromPriorityMessage(request.priority),
          // A partial block is INVALID_ARGUMENT in the domain (FR-683).
          ...(request.schedule === undefined
            ? {}
            : {
                schedule: {
                  windowStart: optionalDate(request.schedule.windowStart),
                  windowEnd: optionalDate(request.schedule.windowEnd),
                  plannedReadyAt: optionalDate(request.schedule.plannedReadyAt),
                },
              }),
        });
        return create(ReceiveOrderRequestResponseSchema, {
          restaurantOrderId: result.restaurantOrder.restaurantOrderId,
          created: result.created,
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async cancelRestaurantOrder(request, context) {
      try {
        const subject = withRequestRevision(
          await subjects.resolve(context),
          context,
        );
        requireField("restaurant_id", request.restaurantId);
        requireField("order_id", request.orderId);
        const result = await dependencies.cancelRestaurantOrder.execute({
          subject,
          restaurantId: request.restaurantId,
          orderId: request.orderId,
          reason: request.reason === "" ? undefined : request.reason,
        });
        // A rejection by stage is a business answer, not a gRPC error.
        return result.outcome === "cancelled"
          ? create(CancelRestaurantOrderResponseSchema, {
              restaurantOrderId: result.restaurantOrderId,
              cancelled: true,
            })
          : create(CancelRestaurantOrderResponseSchema, {
              restaurantOrderId: result.restaurantOrderId,
              cancelled: false,
              rejectionReason: result.reason,
            });
      } catch (error) {
        throw transportError(error);
      }
    },

    // Queue Restaurant Order (FR-224): a scheduled order at its planned start,
    // `service` identity of orders only.
    async queueRestaurantOrder(request, context) {
      try {
        const subject = withRequestRevision(
          await subjects.resolve(context),
          context,
        );
        requireField("restaurant_order_id", request.restaurantOrderId);
        const view = await dependencies.queueRestaurantOrder.execute({
          subject,
          restaurantOrderId: request.restaurantOrderId,
        });
        return create(QueueRestaurantOrderResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    // Record Pickup: the courier took the order, reported by the workflow of
    // orders; `service` identity of orders only.
    async recordRestaurantOrderPickup(request, context) {
      try {
        const subject = withRequestRevision(
          await subjects.resolve(context),
          context,
        );
        requireField("order_id", request.orderId);
        const pickedUpAt = optionalDate(request.pickedUpAt);
        if (pickedUpAt === undefined) {
          throw new ConnectError(
            "picked_up_at is required",
            Code.InvalidArgument,
          );
        }
        const view = await dependencies.recordPickup.execute({
          subject,
          orderId: request.orderId,
          pickedUpAt,
        });
        return create(RecordRestaurantOrderPickupResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    ...createScheduleSlotRpcs(dependencies, subjects),
    ...createKitchenRpcs(dependencies, subjects),
    ...createKitchenBoardRpcs(dependencies, subjects),

    async registerRestaurant(request, context) {
      try {
        const subject = withRequestRevision(
          await subjects.resolve(context),
          context,
        );
        requireField("name", request.name);
        requireField("owner_id", request.ownerId);
        requireField("market_id", request.marketId);
        requireField("time_zone", request.timeZone);
        const result = await dependencies.registerRestaurant.execute({
          subject,
          name: request.name,
          ownerId: request.ownerId,
          marketId: request.marketId,
          timeZone: request.timeZone,
        });
        return create(RegisterRestaurantResponseSchema, {
          restaurant: toRestaurantMessage(result),
          version: BigInt(result.version),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async assignStaff(request, context) {
      try {
        const subject = withRequestRevision(
          await subjects.resolve(context),
          context,
        );
        requireField("restaurant_id", request.restaurantId);
        requireField("user_id", request.userId);
        const result = await dependencies.assignStaff.execute({
          subject,
          restaurantId: request.restaurantId,
          userId: request.userId,
        });
        return create(AssignStaffResponseSchema, toStaffMessage(result));
      } catch (error) {
        throw transportError(error);
      }
    },

    async revokeStaff(request, context) {
      try {
        const subject = withRequestRevision(
          await subjects.resolve(context),
          context,
        );
        requireField("restaurant_id", request.restaurantId);
        requireField("user_id", request.userId);
        const result = await dependencies.revokeStaff.execute({
          subject,
          restaurantId: request.restaurantId,
          userId: request.userId,
        });
        return create(RevokeStaffResponseSchema, toStaffMessage(result));
      } catch (error) {
        throw transportError(error);
      }
    },

    ...createOpeningRpcs(dependencies, subjects),
    ...createMenuRpcs(dependencies, subjects),
    ...createCookPoolRpcs(dependencies, subjects),
    ...createSimulatorRpcs(dependencies, subjects),
  };
};
