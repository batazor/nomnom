import {
  create,
  type DescMessage,
  type MessageInitShape,
  type MessageShape,
} from "@bufbuild/protobuf";
import {
  type Code,
  ConnectError,
  type HandlerContext,
  type ServiceImpl,
} from "@connectrpc/connect";

import { RestaurantsService } from "../../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import {
  createRestaurantsService,
  type RestaurantsServiceDependencies,
} from "../../infrastructure/grpc/service.ts";
import type { SubjectResolver } from "../../infrastructure/grpc/subject.ts";
import type { Subject } from "../../pkg/application/subject.ts";
import { GetRestaurantHandler } from "../../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import { ListRestaurantsHandler } from "../../slices/restaurant/applications/list-restaurants/list-restaurants.ts";
import type { ListIncomingOrdersHandler } from "../../slices/restaurantorder/applications/list-incoming-orders/list-incoming-orders.ts";
import { createKitchenWorld, ordersService } from "./kitchen.ts";

export const context = undefined as never;

type Rpcs = (typeof RestaurantsService)["method"];

/**
 * The service as a caller sees it: requests are built from their init shape
 * like a client does, responses are the full messages the adapter creates.
 */
export type RestaurantsServiceUnderTest = {
  [K in keyof Rpcs]: (
    request: MessageInitShape<Rpcs[K]["input"]>,
    context: HandlerContext,
  ) => Promise<MessageShape<Rpcs[K]["output"]>>;
};

type AnyRpc = (
  request: MessageShape<DescMessage>,
  context: HandlerContext,
) => Promise<MessageShape<DescMessage>>;

const underTest = (
  impl: ServiceImpl<typeof RestaurantsService>,
): RestaurantsServiceUnderTest =>
  Object.fromEntries(
    RestaurantsService.methods.map((method) => {
      const rpc = impl[method.localName as keyof Rpcs] as unknown as AnyRpc;
      return [
        method.localName,
        (request: MessageInitShape<DescMessage>, context: HandlerContext) =>
          rpc(create(method.input, request), context),
      ];
    }),
  ) as RestaurantsServiceUnderTest;

/** createRestaurantsService behind the caller's view of its RPCs. */
export const createService = (
  dependencies: RestaurantsServiceDependencies,
): RestaurantsServiceUnderTest =>
  underTest(createRestaurantsService(dependencies));

export const resolverOf = (subject: Subject): SubjectResolver => ({
  resolve: async () => subject,
});

const notComposed = {
  execute: async (): Promise<never> => {
    throw new Error("query not composed in this test world");
  },
};

/** Service over the MongoDB kitchen world with an `orders-worker` caller. */
export const createWorld = async (
  options: {
    subjects?: SubjectResolver | null;
    /** Query over a fake reader instead of the MongoDB one. */
    listIncomingOrders?: Pick<ListIncomingOrdersHandler, "execute">;
  } = {},
) => {
  const world = await createKitchenWorld();
  const service = createService({
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
