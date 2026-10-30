import type { ServiceImpl } from "@connectrpc/connect";
import {
  type CookPoolRpcDependencies,
  createCookPoolRpcs,
} from "../../slices/cook/infrastructure/grpc/cook-pool.ts";
import {
  createMenuRpcs,
  type MenuRpcDependencies,
} from "../../slices/menu/infrastructure/grpc/menu-commands.ts";
import {
  createMenuQueryRpcs,
  type MenuQueryRpcDependencies,
} from "../../slices/menu/infrastructure/grpc/menu-queries.ts";
import {
  createRestaurantAdminRpcs,
  type RestaurantAdminRpcDependencies,
} from "../../slices/restaurant/infrastructure/grpc/restaurant-admin.ts";
import {
  createOpeningRpcs,
  type OpeningRpcDependencies,
} from "../../slices/restaurant/infrastructure/grpc/restaurant-commands.ts";
import {
  createRestaurantQueryRpcs,
  type RestaurantQueryRpcDependencies,
} from "../../slices/restaurant/infrastructure/grpc/restaurant-queries.ts";
import {
  createFulfillmentRpcs,
  type FulfillmentRpcDependencies,
} from "../../slices/restaurantorder/infrastructure/grpc/fulfillment-commands.ts";
import {
  createKitchenRpcs,
  type KitchenRpcDependencies,
} from "../../slices/restaurantorder/infrastructure/grpc/kitchen.ts";
import {
  createKitchenBoardRpcs,
  type KitchenBoardRpcDependencies,
} from "../../slices/restaurantorder/infrastructure/grpc/kitchen-board.ts";
import {
  createScheduleSlotRpcs,
  type ScheduleSlotRpcDependencies,
} from "../../slices/scheduleslot/infrastructure/grpc/schedule-slots.ts";
import {
  createSimulatorRpcs,
  type SimulatorRpcDependencies,
} from "../../slices/simulator/infrastructure/grpc/simulator.ts";
import type { RestaurantsService } from "./gen/nomnom/restaurants/v1/service_pb.js";
import type { SubjectResolver } from "./subject.ts";

export {
  AUTHORIZATION_REVISION_HEADER,
  withRequestRevision,
} from "./revision.ts";

export type RestaurantsServiceDependencies = RestaurantQueryRpcDependencies &
  RestaurantAdminRpcDependencies &
  OpeningRpcDependencies &
  MenuQueryRpcDependencies &
  MenuRpcDependencies &
  FulfillmentRpcDependencies &
  KitchenRpcDependencies &
  KitchenBoardRpcDependencies &
  ScheduleSlotRpcDependencies &
  CookPoolRpcDependencies &
  SimulatorRpcDependencies &
  Readonly<{ subjects: SubjectResolver }>;

/**
 * Connect/gRPC adapter: the one proto service of the context, assembled from
 * the RPCs its slices own. Generated messages never cross this boundary.
 */
export const createRestaurantsService = (
  dependencies: RestaurantsServiceDependencies,
): ServiceImpl<typeof RestaurantsService> => {
  const subjects = dependencies.subjects;
  return {
    ...createRestaurantQueryRpcs(dependencies, subjects),
    ...createRestaurantAdminRpcs(dependencies, subjects),
    ...createOpeningRpcs(dependencies, subjects),
    ...createMenuQueryRpcs(dependencies),
    ...createMenuRpcs(dependencies, subjects),
    ...createFulfillmentRpcs(dependencies, subjects),
    ...createKitchenRpcs(dependencies, subjects),
    ...createKitchenBoardRpcs(dependencies, subjects),
    ...createScheduleSlotRpcs(dependencies, subjects),
    ...createCookPoolRpcs(dependencies, subjects),
    ...createSimulatorRpcs(dependencies, subjects),
  };
};
