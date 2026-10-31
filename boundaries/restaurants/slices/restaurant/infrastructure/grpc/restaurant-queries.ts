import { create } from "@bufbuild/protobuf";
import type { ServiceImpl } from "@connectrpc/connect";
import {
  requireField,
  transportError,
} from "../../../../infrastructure/grpc/errors.ts";
import {
  BatchGetRestaurantsResponseSchema,
  GetRestaurantResponseSchema,
  ListOwnedRestaurantsResponseSchema,
  ListRestaurantsResponseSchema,
  type RestaurantsService,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import type { SubjectResolver } from "../../../../infrastructure/grpc/subject.ts";
import type { BatchGetRestaurantsHandler } from "../../applications/batch-get/batch-get.ts";
import type { GetRestaurantHandler } from "../../applications/get/get.ts";
import type { ListRestaurantsHandler } from "../../applications/list/list.ts";
import type { ListOwnedRestaurantsHandler } from "../../applications/list-owned/list-owned.ts";
import { toCatalogRestaurantMessage, toRestaurantMessage } from "./messages.ts";

export type RestaurantQueryRpcDependencies = Readonly<{
  batchGetRestaurants: Pick<BatchGetRestaurantsHandler, "execute">;
  getRestaurant: Pick<GetRestaurantHandler, "execute">;
  listOwnedRestaurants: Pick<ListOwnedRestaurantsHandler, "execute">;
  listRestaurants: Pick<ListRestaurantsHandler, "execute">;
}>;

type RestaurantQueryRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  | "getRestaurant"
  | "batchGetRestaurants"
  | "listRestaurants"
  | "listOwnedRestaurants"
>;

export const createRestaurantQueryRpcs = (
  dependencies: RestaurantQueryRpcDependencies,
  subjects: SubjectResolver,
): RestaurantQueryRpcs => ({
  async getRestaurant(request) {
    try {
      const view = await dependencies.getRestaurant.execute({
        restaurantId: request.restaurantId,
      });
      return create(GetRestaurantResponseSchema, {
        restaurant: toCatalogRestaurantMessage(view),
      });
    } catch (error) {
      throw transportError(error);
    }
  },

  async batchGetRestaurants(request) {
    try {
      const views = await dependencies.batchGetRestaurants.execute({
        restaurantIds: request.restaurantIds,
      });
      return create(BatchGetRestaurantsResponseSchema, {
        restaurants: views.map(toCatalogRestaurantMessage),
      });
    } catch (error) {
      throw transportError(error);
    }
  },

  async listRestaurants(request) {
    try {
      // Market is required (ADR 41). Zero values of proto3 mean "not set":
      // default page size, first page.
      requireField("market_id", request.marketId);
      const result = await dependencies.listRestaurants.execute({
        marketId: request.marketId,
        includeClosed: request.includeClosed,
        limit: request.pageSize === 0 ? undefined : request.pageSize,
        cursor: request.pageToken === "" ? undefined : request.pageToken,
      });
      return create(ListRestaurantsResponseSchema, {
        restaurants: result.restaurants.map(toCatalogRestaurantMessage),
        nextPageToken: result.nextCursor ?? "",
      });
    } catch (error) {
      throw transportError(error);
    }
  },

  async listOwnedRestaurants(request, context) {
    try {
      requireField("market_id", request.marketId);
      const result = await dependencies.listOwnedRestaurants.execute({
        subject: await subjects.resolve(context),
        marketId: request.marketId,
        limit: request.pageSize === 0 ? undefined : request.pageSize,
        cursor: request.pageToken === "" ? undefined : request.pageToken,
      });
      return create(ListOwnedRestaurantsResponseSchema, {
        restaurants: result.restaurants.map(toRestaurantMessage),
        nextPageToken: result.nextCursor ?? "",
      });
    } catch (error) {
      throw transportError(error);
    }
  },
});
