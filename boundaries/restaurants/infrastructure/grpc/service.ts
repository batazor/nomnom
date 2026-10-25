import { create } from "@bufbuild/protobuf";
import type { ServiceImpl } from "@connectrpc/connect";
import type { AssignStaffHandler } from "../../slices/restaurant/applications/assign-staff/assign-staff.ts";
import type { GetRestaurantHandler } from "../../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import type { RegisterRestaurantHandler } from "../../slices/restaurant/applications/register-restaurant/register-restaurant.ts";
import type { RevokeStaffHandler } from "../../slices/restaurant/applications/revoke-staff/revoke-staff.ts";
import type { AcceptOrderHandler } from "../../slices/restaurantorder/applications/accept-order/accept-order.ts";
import type { CancelRestaurantOrderHandler } from "../../slices/restaurantorder/applications/cancel-restaurant-order/cancel-restaurant-order.ts";
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
  GetRestaurantResponseSchema,
  ReceiveOrderRequestResponseSchema,
  RegisterRestaurantResponseSchema,
  type RestaurantsService,
  RevokeStaffResponseSchema,
} from "./gen/nomnom/restaurants/v1/service_pb.js";
import { createKitchenRpcs } from "./kitchen.ts";
import { toRestaurantMessage, toStaffMessage } from "./messages.ts";
import { withRequestRevision } from "./revision.ts";
import { RejectingSubjectResolver, type SubjectResolver } from "./subject.ts";

export {
  AUTHORIZATION_REVISION_HEADER,
  withRequestRevision,
} from "./revision.ts";

export type RestaurantsServiceDependencies = Readonly<{
  getRestaurant: Pick<GetRestaurantHandler, "execute">;
  receiveOrderRequest: Pick<ReceiveOrderRequestHandler, "execute">;
  cancelRestaurantOrder: Pick<CancelRestaurantOrderHandler, "execute">;
  acceptRestaurantOrder: Pick<AcceptOrderHandler, "execute">;
  rejectRestaurantOrder: Pick<RejectOrderHandler, "execute">;
  startPreparation: Pick<StartPreparationHandler, "execute">;
  markRestaurantOrderAssembled: Pick<MarkAssembledHandler, "execute">;
  markRestaurantOrderReady: Pick<MarkReadyForPickupHandler, "execute">;
  reportPreparationDelay: Pick<ReportPreparationDelayHandler, "execute">;
  reportPreparationFailure: Pick<ReportPreparationFailureHandler, "execute">;
  registerRestaurant: Pick<RegisterRestaurantHandler, "execute">;
  assignStaff: Pick<AssignStaffHandler, "execute">;
  revokeStaff: Pick<RevokeStaffHandler, "execute">;
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
  };
};
