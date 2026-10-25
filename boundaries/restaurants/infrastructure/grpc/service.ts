import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, type ServiceImpl } from "@connectrpc/connect";
import type { CancelRestaurantOrderHandler } from "../../slices/kitchenorder/applications/cancel-restaurant-order/cancel-restaurant-order.ts";
import type { ReceiveOrderRequestHandler } from "../../slices/kitchenorder/applications/receive-order-request/receive-order-request.ts";
import type { GetRestaurantHandler } from "../../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import { RestaurantSchema } from "./gen/nomnom/restaurants/v1/restaurants_pb.js";
import {
  CancelRestaurantOrderResponseSchema,
  GetRestaurantResponseSchema,
  ReceiveOrderRequestResponseSchema,
  type RestaurantsService,
} from "./gen/nomnom/restaurants/v1/service_pb.js";
import { RejectingSubjectResolver, type SubjectResolver } from "./subject.ts";

export type RestaurantsServiceDependencies = Readonly<{
  getRestaurant: Pick<GetRestaurantHandler, "execute">;
  receiveOrderRequest: Pick<ReceiveOrderRequestHandler, "execute">;
  cancelRestaurantOrder: Pick<CancelRestaurantOrderHandler, "execute">;
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
          restaurant: create(RestaurantSchema, {
            restaurantId: view.restaurantId,
            name: view.name,
            isOpen: view.isOpen,
            address: view.address,
          }),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async receiveOrderRequest(request, context) {
      try {
        const subject = await subjects.resolve(context);
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
        const subject = await subjects.resolve(context);
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
  };
};

function requireField(name: string, value: string): void {
  if (value.trim().length === 0) {
    throw new ConnectError(`${name} is required`, Code.InvalidArgument);
  }
}

/** gRPC status of application and domain errors by their stable `code`. */
const STATUS_BY_ERROR_CODE: Readonly<Record<string, Code>> = {
  not_found: Code.NotFound,
  permission_denied: Code.PermissionDenied,
  authorization_unavailable: Code.Unavailable,
  already_exists: Code.AlreadyExists,
  duplicate_order_id: Code.AlreadyExists,
  concurrency_conflict: Code.Aborted,
  invalid_restaurant: Code.InvalidArgument,
  invalid_restaurant_order: Code.InvalidArgument,
  invalid_quantity: Code.InvalidArgument,
  invalid_transition: Code.FailedPrecondition,
  guard_failed: Code.FailedPrecondition,
  already_in_state: Code.FailedPrecondition,
};

function transportError(error: unknown): ConnectError {
  if (error instanceof ConnectError) return error;
  if (error instanceof Error && "code" in error) {
    const status = STATUS_BY_ERROR_CODE[String(error.code)];
    if (status !== undefined) return new ConnectError(error.message, status);
  }
  return new ConnectError("internal error", Code.Internal);
}
