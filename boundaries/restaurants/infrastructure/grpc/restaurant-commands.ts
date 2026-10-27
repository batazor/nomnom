import { create } from "@bufbuild/protobuf";
import type { HandlerContext, ServiceImpl } from "@connectrpc/connect";
import { Code, ConnectError } from "@connectrpc/connect";
import type { RestaurantChangeResult } from "../../slices/restaurant/applications/restaurant-view.ts";
import type { PremiumFeeChange } from "../../slices/restaurant/applications/update-restaurant/update-restaurant.ts";
import { requireField, transportError } from "./errors.ts";
import type { Money } from "./gen/nomnom/restaurants/v1/restaurants_pb.js";
import {
  CloseRestaurantResponseSchema,
  OpenRestaurantResponseSchema,
  type RestaurantsService,
  UpdateRestaurantResponseSchema,
} from "./gen/nomnom/restaurants/v1/service_pb.js";
import { toRestaurantMessage } from "./messages.ts";
import { withRequestRevision } from "./revision.ts";
import type { RestaurantsServiceDependencies } from "./service.ts";
import type { SubjectResolver } from "./subject.ts";

type OpeningRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  "openRestaurant" | "closeRestaurant" | "updateRestaurant"
>;

/** premium_fee sets, clear_premium_fee removes, neither keeps (FR-649). */
const premiumFeeChange = (request: {
  premiumFee?: Money;
  clearPremiumFee: boolean;
}): PremiumFeeChange => {
  if (request.premiumFee !== undefined && request.clearPremiumFee) {
    throw new ConnectError(
      "premium_fee and clear_premium_fee are mutually exclusive",
      Code.InvalidArgument,
    );
  }
  if (request.clearPremiumFee) return { kind: "clear" };
  if (request.premiumFee === undefined) return { kind: "keep" };
  return {
    kind: "set",
    fee: {
      amount: request.premiumFee.amount,
      currency: request.premiumFee.currency,
    },
  };
};

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

    // Update Restaurant (FR-202, FR-649): name and premium fee, `restaurant#manage`.
    async updateRestaurant(request, context) {
      try {
        const { subject, restaurantId } = await openingCommand(
          request,
          context,
        );
        requireField("name", request.name);
        const result = await dependencies.updateRestaurant.execute({
          subject,
          restaurantId,
          name: request.name,
          premiumFee: premiumFeeChange(request),
        });
        return create(
          UpdateRestaurantResponseSchema,
          toOpeningResponse(result),
        );
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
