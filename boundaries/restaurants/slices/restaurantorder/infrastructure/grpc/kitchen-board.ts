import { create } from "@bufbuild/protobuf";
import type { HandlerContext, ServiceImpl } from "@connectrpc/connect";
import {
  requireField,
  transportError,
} from "../../../../infrastructure/grpc/errors.ts";
import {
  GetKitchenQueueResponseSchema,
  ListActiveRestaurantOrdersResponseSchema,
  ListIncomingOrdersResponseSchema,
  ListScheduledOrdersResponseSchema,
  type RestaurantsService,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { withRequestRevision } from "../../../../infrastructure/grpc/revision.ts";
import type { SubjectResolver } from "../../../../infrastructure/grpc/subject.ts";
import type { GetKitchenQueueHandler } from "../../applications/get-kitchen-queue/get-kitchen-queue.ts";
import type { KitchenBoardQuery } from "../../applications/kitchen-board/kitchen-board.ts";
import type { ListActiveRestaurantOrdersHandler } from "../../applications/list-active-restaurant-orders/list-active-restaurant-orders.ts";
import type { ListIncomingOrdersHandler } from "../../applications/list-incoming-orders/list-incoming-orders.ts";
import type { ListScheduledOrdersHandler } from "../../applications/list-scheduled-orders/list-scheduled-orders.ts";
import {
  toActiveRestaurantOrderMessage,
  toIncomingOrderMessage,
  toKitchenQueueEntryMessage,
  toScheduledOrderMessage,
} from "./messages.ts";

export type KitchenBoardRpcDependencies = Readonly<{
  getKitchenQueue: Pick<GetKitchenQueueHandler, "execute">;
  listActiveRestaurantOrders: Pick<
    ListActiveRestaurantOrdersHandler,
    "execute"
  >;
  listIncomingOrders: Pick<ListIncomingOrdersHandler, "execute">;
  listScheduledOrders: Pick<ListScheduledOrdersHandler, "execute">;
}>;

type KitchenBoardRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  | "getKitchenQueue"
  | "listIncomingOrders"
  | "listScheduledOrders"
  | "listActiveRestaurantOrders"
>;

/**
 * Kitchen board queries of restaurant staff (FR-583, FR-586, FR-684, active orders): read-only, they
 * require `restaurant#operate_kitchen` through the query's own access port.
 */
export const createKitchenBoardRpcs = (
  dependencies: KitchenBoardRpcDependencies,
  subjects: SubjectResolver,
): KitchenBoardRpcs => {
  /** Resolves the caller and the restaurant of a kitchen board query. */
  const boardQuery = async (
    request: { restaurantId: string },
    context: HandlerContext,
  ): Promise<KitchenBoardQuery> => {
    const subject = withRequestRevision(
      await subjects.resolve(context),
      context,
    );
    requireField("restaurant_id", request.restaurantId);
    return { subject, restaurantId: request.restaurantId };
  };
  return {
    async getKitchenQueue(request, context) {
      try {
        const queue = await dependencies.getKitchenQueue.execute(
          await boardQuery(request, context),
        );
        return create(GetKitchenQueueResponseSchema, {
          entries: queue.entries.map((entry) =>
            toKitchenQueueEntryMessage(queue.restaurantId, entry),
          ),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async listIncomingOrders(request, context) {
      try {
        const incoming = await dependencies.listIncomingOrders.execute(
          await boardQuery(request, context),
        );
        return create(ListIncomingOrdersResponseSchema, {
          orders: incoming.orders.map((order) =>
            toIncomingOrderMessage(incoming.restaurantId, order),
          ),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    // Accepted scheduled orders waiting for their start (FR-684).
    async listScheduledOrders(request, context) {
      try {
        const scheduled = await dependencies.listScheduledOrders.execute(
          await boardQuery(request, context),
        );
        return create(ListScheduledOrdersResponseSchema, {
          orders: scheduled.orders.map(toScheduledOrderMessage),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async listActiveRestaurantOrders(request, context) {
      try {
        const active = await dependencies.listActiveRestaurantOrders.execute(
          await boardQuery(request, context),
        );
        return create(ListActiveRestaurantOrdersResponseSchema, {
          orders: active.orders.map((order) =>
            toActiveRestaurantOrderMessage(active.restaurantId, order),
          ),
        });
      } catch (error) {
        throw transportError(error);
      }
    },
  };
};
