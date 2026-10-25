import { create } from "@bufbuild/protobuf";
import type { HandlerContext, ServiceImpl } from "@connectrpc/connect";
import type { KitchenBoardQuery } from "../../slices/restaurantorder/applications/kitchen-board.ts";
import { requireField, transportError } from "./errors.ts";
import {
  GetKitchenQueueResponseSchema,
  ListActiveRestaurantOrdersResponseSchema,
  ListIncomingOrdersResponseSchema,
  type RestaurantsService,
} from "./gen/nomnom/restaurants/v1/service_pb.js";
import {
  toActiveRestaurantOrderMessage,
  toIncomingOrderMessage,
  toKitchenQueueEntryMessage,
} from "./messages.ts";
import { withRequestRevision } from "./revision.ts";
import type { RestaurantsServiceDependencies } from "./service.ts";
import type { SubjectResolver } from "./subject.ts";

type KitchenBoardRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  "getKitchenQueue" | "listIncomingOrders" | "listActiveRestaurantOrders"
>;

/**
 * Kitchen board queries of restaurant staff (FR-583, FR-586, active orders): read-only, they
 * require `restaurant#operate_kitchen` through the query's own access port.
 */
export const createKitchenBoardRpcs = (
  dependencies: RestaurantsServiceDependencies,
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
