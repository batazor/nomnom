import { create } from "@bufbuild/protobuf";
import type { ServiceImpl } from "@connectrpc/connect";
import { requireField, transportError } from "./errors.ts";
import {
  GetKitchenQueueResponseSchema,
  type RestaurantsService,
} from "./gen/nomnom/restaurants/v1/service_pb.js";
import { toKitchenQueueEntryMessage } from "./messages.ts";
import { withRequestRevision } from "./revision.ts";
import type { RestaurantsServiceDependencies } from "./service.ts";
import type { SubjectResolver } from "./subject.ts";

type KitchenBoardRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  "getKitchenQueue"
>;

/**
 * Kitchen board queries of restaurant staff (FR-583): read-only, they require
 * `restaurant#operate_kitchen` through the query's own access port.
 */
export const createKitchenBoardRpcs = (
  dependencies: RestaurantsServiceDependencies,
  subjects: SubjectResolver,
): KitchenBoardRpcs => ({
  async getKitchenQueue(request, context) {
    try {
      const subject = withRequestRevision(
        await subjects.resolve(context),
        context,
      );
      requireField("restaurant_id", request.restaurantId);
      const queue = await dependencies.getKitchenQueue.execute({
        subject,
        restaurantId: request.restaurantId,
      });
      return create(GetKitchenQueueResponseSchema, {
        entries: queue.entries.map((entry) =>
          toKitchenQueueEntryMessage(queue.restaurantId, entry),
        ),
      });
    } catch (error) {
      throw transportError(error);
    }
  },
});
