import { create } from "@bufbuild/protobuf";
import type { HandlerContext, ServiceImpl } from "@connectrpc/connect";
import type { RestaurantChangeResult } from "../../slices/restaurant/applications/restaurant-view.ts";
import { requireField, transportError } from "./errors.ts";
import {
  CloseRestaurantResponseSchema,
  OpenRestaurantResponseSchema,
  type RestaurantsService,
} from "./gen/nomnom/restaurants/v1/service_pb.js";
import { toRestaurantMessage } from "./messages.ts";
import { withRequestRevision } from "./revision.ts";
import type { RestaurantsServiceDependencies } from "./service.ts";
import type { SubjectResolver } from "./subject.ts";

type OpeningRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  "openRestaurant" | "closeRestaurant"
>;

const toOpeningResponse = (result: RestaurantChangeResult) => ({
  restaurant: toRestaurantMessage(result),
  version: BigInt(result.version),
  changed: result.changed,
});

/**
 * Open/Close Restaurant (FR-203): `restaurant#manage`, a repeat is a
 * successful no-op with `changed = false` and no new fact (AC-209).
 */
export const createOpeningRpcs = (
  dependencies: RestaurantsServiceDependencies,
  subjects: SubjectResolver,
): OpeningRpcs => {
  const openingCommand = async (
    request: { restaurantId: string },
    context: HandlerContext,
  ) => {
    const subject = withRequestRevision(
      await subjects.resolve(context),
      context,
    );
    requireField("restaurant_id", request.restaurantId);
    return { subject, restaurantId: request.restaurantId };
  };
  return {
    async openRestaurant(request, context) {
      try {
        const result = await dependencies.openRestaurant.execute(
          await openingCommand(request, context),
        );
        return create(OpenRestaurantResponseSchema, toOpeningResponse(result));
      } catch (error) {
        throw transportError(error);
      }
    },

    async closeRestaurant(request, context) {
      try {
        const result = await dependencies.closeRestaurant.execute(
          await openingCommand(request, context),
        );
        return create(CloseRestaurantResponseSchema, toOpeningResponse(result));
      } catch (error) {
        throw transportError(error);
      }
    },
  };
};
