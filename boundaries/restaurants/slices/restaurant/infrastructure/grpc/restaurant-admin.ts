import { create } from "@bufbuild/protobuf";
import type { ServiceImpl } from "@connectrpc/connect";
import {
  requireField,
  transportError,
} from "../../../../infrastructure/grpc/errors.ts";
import {
  AssignStaffResponseSchema,
  RegisterRestaurantResponseSchema,
  type RestaurantsService,
  RevokeStaffResponseSchema,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { withRequestRevision } from "../../../../infrastructure/grpc/revision.ts";
import type { SubjectResolver } from "../../../../infrastructure/grpc/subject.ts";
import type { AssignStaffHandler } from "../../applications/assign-staff/assign-staff.ts";
import type { RegisterRestaurantHandler } from "../../applications/register-restaurant/register-restaurant.ts";
import type { RevokeStaffHandler } from "../../applications/revoke-staff/revoke-staff.ts";
import { toRestaurantMessage, toStaffMessage } from "./messages.ts";

export type RestaurantAdminRpcDependencies = Readonly<{
  assignStaff: Pick<AssignStaffHandler, "execute">;
  registerRestaurant: Pick<RegisterRestaurantHandler, "execute">;
  revokeStaff: Pick<RevokeStaffHandler, "execute">;
}>;

type RestaurantAdminRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  "registerRestaurant" | "assignStaff" | "revokeStaff"
>;

export const createRestaurantAdminRpcs = (
  dependencies: RestaurantAdminRpcDependencies,
  subjects: SubjectResolver,
): RestaurantAdminRpcs => ({
  async registerRestaurant(request, context) {
    try {
      const subject = withRequestRevision(
        await subjects.resolve(context),
        context,
      );
      requireField("name", request.name);
      requireField("owner_id", request.ownerId);
      requireField("market_id", request.marketId);
      requireField("time_zone", request.timeZone);
      const result = await dependencies.registerRestaurant.execute({
        subject,
        name: request.name,
        ownerId: request.ownerId,
        marketId: request.marketId,
        timeZone: request.timeZone,
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
});
