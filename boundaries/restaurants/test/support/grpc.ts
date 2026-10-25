import { type Code, ConnectError } from "@connectrpc/connect";

import { createRestaurantsService } from "../../infrastructure/grpc/service.ts";
import type { SubjectResolver } from "../../infrastructure/grpc/subject.ts";
import type { Subject } from "../../pkg/application/subject.ts";
import { GetRestaurantHandler } from "../../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import { ListRestaurantsHandler } from "../../slices/restaurant/applications/list-restaurants/list-restaurants.ts";
import type { ListIncomingOrdersHandler } from "../../slices/restaurantorder/applications/list-incoming-orders/list-incoming-orders.ts";
import { createKitchenWorld, ordersService } from "./kitchen.ts";

export const context = undefined as never;

export const resolverOf = (subject: Subject): SubjectResolver => ({
  resolve: async () => subject,
});

const notComposed = {
  execute: async (): Promise<never> => {
    throw new Error("query not composed in this test world");
  },
};

/** Service over the in-memory kitchen world with an `orders-worker` caller. */
export const createWorld = (
  options: {
    subjects?: SubjectResolver | null;
    /** Query over a fake reader; the MongoDB reader has no memory variant. */
    listIncomingOrders?: Pick<ListIncomingOrdersHandler, "execute">;
  } = {},
) => {
  const world = createKitchenWorld();
  const service = createRestaurantsService({
    getRestaurant: new GetRestaurantHandler({ reader: world.catalog }),
    getMenu: world.getMenu,
    listRestaurants: new ListRestaurantsHandler({ reader: world.catalog }),
    receiveOrderRequest: world.receive,
    cancelRestaurantOrder: world.cancel,
    acceptRestaurantOrder: world.accept,
    rejectRestaurantOrder: world.reject,
    startPreparation: world.startPreparation,
    markRestaurantOrderAssembled: world.markAssembled,
    markRestaurantOrderReady: world.markReady,
    reportPreparationDelay: world.reportDelay,
    reportPreparationFailure: world.reportFailure,
    getKitchenQueue: world.getKitchenQueue,
    listIncomingOrders: options.listIncomingOrders ?? notComposed,
    registerRestaurant: world.register,
    assignStaff: world.assignStaff,
    revokeStaff: world.revokeStaff,
    openRestaurant: world.open,
    closeRestaurant: world.close,
    createMenu: world.createMenu,
    addMenuItem: world.addMenuItem,
    updateMenuItem: world.updateMenuItem,
    setMenuItemAvailability: world.setAvailability,
    subjects:
      options.subjects === null
        ? undefined
        : (options.subjects ?? resolverOf(ordersService)),
  });
  return { world, service };
};

export const rejectsWith = (code: Code) => (error: unknown) =>
  error instanceof ConnectError && error.code === code;
