import { create } from "@bufbuild/protobuf";
import type { ServiceImpl } from "@connectrpc/connect";
import type { AddMenuItemHandler } from "../../slices/menu/applications/add-menu-item/add-menu-item.ts";
import type { CreateMenuHandler } from "../../slices/menu/applications/create-menu/create-menu.ts";
import type { GetMenuHandler } from "../../slices/menu/applications/get-menu/get-menu.ts";
import type { SetMenuItemAvailabilityHandler } from "../../slices/menu/applications/set-menu-item-availability/set-menu-item-availability.ts";
import type { UpdateMenuItemHandler } from "../../slices/menu/applications/update-menu-item/update-menu-item.ts";
import type { AssignStaffHandler } from "../../slices/restaurant/applications/assign-staff/assign-staff.ts";
import type { CloseRestaurantHandler } from "../../slices/restaurant/applications/close-restaurant/close-restaurant.ts";
import type { GetRestaurantHandler } from "../../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import type { ListRestaurantsHandler } from "../../slices/restaurant/applications/list-restaurants/list-restaurants.ts";
import type { OpenRestaurantHandler } from "../../slices/restaurant/applications/open-restaurant/open-restaurant.ts";
import type { RegisterRestaurantHandler } from "../../slices/restaurant/applications/register-restaurant/register-restaurant.ts";
import type { RevokeStaffHandler } from "../../slices/restaurant/applications/revoke-staff/revoke-staff.ts";
import type { AcceptOrderHandler } from "../../slices/restaurantorder/applications/accept-order/accept-order.ts";
import type { CancelRestaurantOrderHandler } from "../../slices/restaurantorder/applications/cancel-restaurant-order/cancel-restaurant-order.ts";
import type { GetKitchenQueueHandler } from "../../slices/restaurantorder/applications/get-kitchen-queue/get-kitchen-queue.ts";
import type { ListIncomingOrdersHandler } from "../../slices/restaurantorder/applications/list-incoming-orders/list-incoming-orders.ts";
import type { MarkAssembledHandler } from "../../slices/restaurantorder/applications/mark-assembled/mark-assembled.ts";
import type { MarkReadyForPickupHandler } from "../../slices/restaurantorder/applications/mark-ready-for-pickup/mark-ready-for-pickup.ts";
import type { ReceiveOrderRequestHandler } from "../../slices/restaurantorder/applications/receive-order-request/receive-order-request.ts";
import type { RejectOrderHandler } from "../../slices/restaurantorder/applications/reject-order/reject-order.ts";
import type { ReportPreparationDelayHandler } from "../../slices/restaurantorder/applications/report-preparation-delay/report-preparation-delay.ts";
import type { ReportPreparationFailureHandler } from "../../slices/restaurantorder/applications/report-preparation-failure/report-preparation-failure.ts";
import type { StartPreparationHandler } from "../../slices/restaurantorder/applications/start-preparation/start-preparation.ts";
import { requireField, transportError } from "./errors.ts";
import {
  AssignStaffResponseSchema,
  CancelRestaurantOrderResponseSchema,
  GetMenuResponseSchema,
  GetRestaurantResponseSchema,
  ListRestaurantsResponseSchema,
  ReceiveOrderRequestResponseSchema,
  RegisterRestaurantResponseSchema,
  type RestaurantsService,
  RevokeStaffResponseSchema,
} from "./gen/nomnom/restaurants/v1/service_pb.js";
import { createKitchenRpcs } from "./kitchen.ts";
import { createKitchenBoardRpcs } from "./kitchen-board.ts";
import { createMenuRpcs } from "./menu-commands.ts";
import {
  toMenuItemMessages,
  toRestaurantMessage,
  toStaffMessage,
} from "./messages.ts";
import { createOpeningRpcs } from "./restaurant-commands.ts";
import { withRequestRevision } from "./revision.ts";
import { RejectingSubjectResolver, type SubjectResolver } from "./subject.ts";

export {
  AUTHORIZATION_REVISION_HEADER,
  withRequestRevision,
} from "./revision.ts";

export type RestaurantsServiceDependencies = Readonly<{
  getRestaurant: Pick<GetRestaurantHandler, "execute">;
  getMenu: Pick<GetMenuHandler, "execute">;
  listRestaurants: Pick<ListRestaurantsHandler, "execute">;
  receiveOrderRequest: Pick<ReceiveOrderRequestHandler, "execute">;
  cancelRestaurantOrder: Pick<CancelRestaurantOrderHandler, "execute">;
  acceptRestaurantOrder: Pick<AcceptOrderHandler, "execute">;
  rejectRestaurantOrder: Pick<RejectOrderHandler, "execute">;
  startPreparation: Pick<StartPreparationHandler, "execute">;
  markRestaurantOrderAssembled: Pick<MarkAssembledHandler, "execute">;
  markRestaurantOrderReady: Pick<MarkReadyForPickupHandler, "execute">;
  reportPreparationDelay: Pick<ReportPreparationDelayHandler, "execute">;
  reportPreparationFailure: Pick<ReportPreparationFailureHandler, "execute">;
  getKitchenQueue: Pick<GetKitchenQueueHandler, "execute">;
  listIncomingOrders: Pick<ListIncomingOrdersHandler, "execute">;
  registerRestaurant: Pick<RegisterRestaurantHandler, "execute">;
  assignStaff: Pick<AssignStaffHandler, "execute">;
  revokeStaff: Pick<RevokeStaffHandler, "execute">;
  openRestaurant: Pick<OpenRestaurantHandler, "execute">;
  closeRestaurant: Pick<CloseRestaurantHandler, "execute">;
  createMenu: Pick<CreateMenuHandler, "execute">;
  addMenuItem: Pick<AddMenuItemHandler, "execute">;
  updateMenuItem: Pick<UpdateMenuItemHandler, "execute">;
  setMenuItemAvailability: Pick<SetMenuItemAvailabilityHandler, "execute">;
  /** Defaults to {@link RejectingSubjectResolver}: commands fail UNAUTHENTICATED. */
  subjects?: SubjectResolver;
}>;

/** Connect/gRPC adapter. Generated messages never cross this boundary. */
export const createRestaurantsService = (
  dependencies: RestaurantsServiceDependencies,
): ServiceImpl<typeof RestaurantsService> => {
  const subjects = dependencies.subjects ?? new RejectingSubjectResolver();
  return {
    async getRestaurant(request) {
      try {
        const view = await dependencies.getRestaurant.execute({
          restaurantId: request.restaurantId,
        });
        return create(GetRestaurantResponseSchema, {
          restaurant: toRestaurantMessage(view),
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

    async listRestaurants(request) {
      try {
        // Zero values of proto3 mean "not set": default page size, first page.
        const result = await dependencies.listRestaurants.execute({
          includeClosed: request.includeClosed,
          limit: request.pageSize === 0 ? undefined : request.pageSize,
          cursor: request.pageToken === "" ? undefined : request.pageToken,
        });
        return create(ListRestaurantsResponseSchema, {
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
        requireField("address", request.address);
        const result = await dependencies.registerRestaurant.execute({
          subject,
          name: request.name,
          ownerId: request.ownerId,
          address: request.address,
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
  };
};
