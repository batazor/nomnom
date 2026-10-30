import { create } from "@bufbuild/protobuf";
import { type Timestamp, timestampDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError, type ServiceImpl } from "@connectrpc/connect";
import {
  requireField,
  transportError,
} from "../../../../infrastructure/grpc/errors.ts";
import {
  CancelRestaurantOrderResponseSchema,
  QueueRestaurantOrderResponseSchema,
  ReceiveOrderRequestResponseSchema,
  RecordRestaurantOrderPickupResponseSchema,
  type RestaurantsService,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { withRequestRevision } from "../../../../infrastructure/grpc/revision.ts";
import type { SubjectResolver } from "../../../../infrastructure/grpc/subject.ts";
import type { CancelRestaurantOrderHandler } from "../../applications/cancel-restaurant-order/cancel-restaurant-order.ts";
import type { QueueRestaurantOrderHandler } from "../../applications/queue-restaurant-order/queue-restaurant-order.ts";
import type { ReceiveOrderRequestHandler } from "../../applications/receive-order-request/receive-order-request.ts";
import type { RecordPickupHandler } from "../../applications/record-pickup/record-pickup.ts";
import { fromPriorityMessage, toRestaurantOrderMessage } from "./messages.ts";

export type FulfillmentRpcDependencies = Readonly<{
  cancelRestaurantOrder: Pick<CancelRestaurantOrderHandler, "execute">;
  queueRestaurantOrder: Pick<QueueRestaurantOrderHandler, "execute">;
  receiveOrderRequest: Pick<ReceiveOrderRequestHandler, "execute">;
  recordPickup: Pick<RecordPickupHandler, "execute">;
}>;

type FulfillmentRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  | "receiveOrderRequest"
  | "cancelRestaurantOrder"
  | "queueRestaurantOrder"
  | "recordRestaurantOrderPickup"
>;

const optionalDate = (value: Timestamp | undefined): Date | undefined =>
  value === undefined ? undefined : timestampDate(value);

export const createFulfillmentRpcs = (
  dependencies: FulfillmentRpcDependencies,
  subjects: SubjectResolver,
): FulfillmentRpcs => ({
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
});
