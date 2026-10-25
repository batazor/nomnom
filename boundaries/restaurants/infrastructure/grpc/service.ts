import { create } from "@bufbuild/protobuf";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  Code,
  ConnectError,
  type HandlerContext,
  type ServiceImpl,
} from "@connectrpc/connect";
import type { Subject } from "../../pkg/application/subject.ts";
import type { AcceptOrderHandler } from "../../slices/kitchenorder/applications/accept-order/accept-order.ts";
import type { CancelRestaurantOrderHandler } from "../../slices/kitchenorder/applications/cancel-restaurant-order/cancel-restaurant-order.ts";
import type { MarkAssembledHandler } from "../../slices/kitchenorder/applications/mark-assembled/mark-assembled.ts";
import type { MarkReadyForPickupHandler } from "../../slices/kitchenorder/applications/mark-order-ready/mark-order-ready.ts";
import type { ReceiveOrderRequestHandler } from "../../slices/kitchenorder/applications/receive-order-request/receive-order-request.ts";
import type { RejectOrderHandler } from "../../slices/kitchenorder/applications/reject-order/reject-order.ts";
import type { ReportPreparationDelayHandler } from "../../slices/kitchenorder/applications/report-preparation-delay/report-preparation-delay.ts";
import type { ReportPreparationFailureHandler } from "../../slices/kitchenorder/applications/report-preparation-failure/report-preparation-failure.ts";
import type {
  KitchenCommand,
  RestaurantOrderView,
} from "../../slices/kitchenorder/applications/restaurant-order-support.ts";
import type { StartPreparationHandler } from "../../slices/kitchenorder/applications/start-preparing-order/start-preparing-order.ts";
import type { AssignStaffHandler } from "../../slices/restaurant/applications/assign-staff/assign-staff.ts";
import type { StaffChangeResult } from "../../slices/restaurant/applications/change-staff.ts";
import type { GetRestaurantHandler } from "../../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import type { RegisterRestaurantHandler } from "../../slices/restaurant/applications/register-restaurant/register-restaurant.ts";
import type { RestaurantView } from "../../slices/restaurant/applications/restaurant-view.ts";
import type { RevokeStaffHandler } from "../../slices/restaurant/applications/revoke-staff/revoke-staff.ts";
import { RestaurantSchema } from "./gen/nomnom/restaurants/v1/restaurants_pb.js";
import {
  AcceptRestaurantOrderResponseSchema,
  AssignStaffResponseSchema,
  CancelRestaurantOrderResponseSchema,
  GetRestaurantResponseSchema,
  MarkRestaurantOrderAssembledResponseSchema,
  MarkRestaurantOrderReadyResponseSchema,
  ReceiveOrderRequestResponseSchema,
  RegisterRestaurantResponseSchema,
  RejectRestaurantOrderResponseSchema,
  ReportPreparationDelayResponseSchema,
  ReportPreparationFailureResponseSchema,
  RestaurantOrderSchema,
  RestaurantOrderState,
  type RestaurantsService,
  RevokeStaffResponseSchema,
  StartPreparationResponseSchema,
} from "./gen/nomnom/restaurants/v1/service_pb.js";
import { RejectingSubjectResolver, type SubjectResolver } from "./subject.ts";

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
  /** Resolves the caller and the addressed Restaurant Order of a kitchen command. */
  const kitchenCommand = async (
    request: { restaurantId: string; restaurantOrderId: string },
    context: HandlerContext,
  ): Promise<KitchenCommand> => {
    const subject = withRequestRevision(
      await subjects.resolve(context),
      context,
    );
    requireField("restaurant_id", request.restaurantId);
    requireField("restaurant_order_id", request.restaurantOrderId);
    return {
      subject,
      restaurantId: request.restaurantId,
      restaurantOrderId: request.restaurantOrderId,
    };
  };
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

    async acceptRestaurantOrder(request, context) {
      try {
        const command = await kitchenCommand(request, context);
        const view = await dependencies.acceptRestaurantOrder.execute(command);
        return create(AcceptRestaurantOrderResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async rejectRestaurantOrder(request, context) {
      try {
        const command = await kitchenCommand(request, context);
        const view = await dependencies.rejectRestaurantOrder.execute({
          ...command,
          reason: request.reason === "" ? undefined : request.reason,
        });
        return create(RejectRestaurantOrderResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async startPreparation(request, context) {
      try {
        const command = await kitchenCommand(request, context);
        const view = await dependencies.startPreparation.execute(command);
        return create(StartPreparationResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async markRestaurantOrderAssembled(request, context) {
      try {
        const command = await kitchenCommand(request, context);
        const view =
          await dependencies.markRestaurantOrderAssembled.execute(command);
        return create(MarkRestaurantOrderAssembledResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async markRestaurantOrderReady(request, context) {
      try {
        const command = await kitchenCommand(request, context);
        const view =
          await dependencies.markRestaurantOrderReady.execute(command);
        return create(MarkRestaurantOrderReadyResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async reportPreparationDelay(request, context) {
      try {
        const command = await kitchenCommand(request, context);
        if (request.expectedReadyAt === undefined) {
          throw new ConnectError(
            "expected_ready_at is required",
            Code.InvalidArgument,
          );
        }
        const view = await dependencies.reportPreparationDelay.execute({
          ...command,
          expectedReadyAt: timestampDate(request.expectedReadyAt),
          reason: request.reason === "" ? undefined : request.reason,
        });
        return create(ReportPreparationDelayResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async reportPreparationFailure(request, context) {
      try {
        const command = await kitchenCommand(request, context);
        requireField("reason", request.reason);
        const view = await dependencies.reportPreparationFailure.execute({
          ...command,
          reason: request.reason,
        });
        return create(ReportPreparationFailureResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

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

const toRestaurantMessage = (view: RestaurantView) =>
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

const toRestaurantOrderMessage = (view: RestaurantOrderView) =>
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

const toStaffMessage = (result: StaffChangeResult) => ({
  restaurantId: result.restaurantId,
  staff: [...result.staff],
  changed: result.changed,
});

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

/** gRPC metadata carrying the caller's authorization revision (docs/security.md). */
export const AUTHORIZATION_REVISION_HEADER = "authorization-revision";
const MAX_REVISION_LENGTH = 1024;

/**
 * Attaches the caller's ZedToken from metadata to the verified subject. An
 * absent or oversized value is ignored: the revision only affects freshness.
 */
export function withRequestRevision(
  subject: Subject,
  context: { requestHeader?: Headers } | undefined,
): Subject {
  const revision = context?.requestHeader
    ?.get(AUTHORIZATION_REVISION_HEADER)
    ?.trim();
  if (!revision || revision.length > MAX_REVISION_LENGTH) {
    return subject;
  }
  return { ...subject, authorizationRevision: revision };
}
