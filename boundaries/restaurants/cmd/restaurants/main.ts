import { randomUUID } from "node:crypto";
import { createServer } from "node:http2";

import { connectNodeAdapter } from "@connectrpc/connect-node";

import { RestaurantsService } from "../../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { createRestaurantsService } from "../../infrastructure/grpc/service.ts";
import { createSubjectResolver } from "../../infrastructure/grpc/subject.ts";
import { GrpcWorker } from "../../infrastructure/grpc/worker.ts";
import { loadConfig, loadLocalEnv } from "../../pkg/config/config.ts";
import { InMemoryOutbox } from "../../pkg/infrastructure/memory/outbox.ts";
import { InMemoryUnitOfWork } from "../../pkg/infrastructure/memory/unit-of-work.ts";
import { Lifecycle } from "../../pkg/lifecycle/lifecycle.ts";
import { CancelRestaurantOrderHandler } from "../../slices/kitchenorder/applications/cancel-restaurant-order/cancel-restaurant-order.ts";
import { ReceiveOrderRequestHandler } from "../../slices/kitchenorder/applications/receive-order-request/receive-order-request.ts";
import {
  createRestaurantOrderStore,
  InMemoryKitchenAccessControl,
  InMemoryRestaurantOrderRepository,
} from "../../slices/kitchenorder/infrastructure/memory/restaurant-orders.ts";
import { GetRestaurantHandler } from "../../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import {
  createRestaurantStore,
  InMemoryRestaurantCatalog,
  InMemoryRestaurantRepository,
} from "../../slices/restaurant/infrastructure/memory/restaurants.ts";

loadLocalEnv();
const config = loadConfig();
// Projected ServiceAccount token verification (ADR 27). Without issuer,
// audience and JWKS the RejectingSubjectResolver keeps commands fail closed;
// an invalid JWKS or allowlist stops startup.
const subjects = await createSubjectResolver(config.identity, (message) =>
  console.warn(`restaurants ${message}`),
);

const clock = { now: () => new Date() };
const unitOfWork = new InMemoryUnitOfWork();
const outbox = new InMemoryOutbox(unitOfWork);
const restaurantStore = createRestaurantStore();
const orderStore = createRestaurantOrderStore();
unitOfWork.enlist(restaurantStore);
unitOfWork.enlist(orderStore);
const restaurants = new InMemoryRestaurantRepository(restaurantStore);
const restaurantOrders = new InMemoryRestaurantOrderRepository(orderStore);
// No grants until SpiceDB is wired: fulfillment commands fail closed.
const kitchenAccessControl = new InMemoryKitchenAccessControl();

const service = createRestaurantsService({
  subjects,
  getRestaurant: new GetRestaurantHandler({
    reader: new InMemoryRestaurantCatalog(restaurantStore),
  }),
  receiveOrderRequest: new ReceiveOrderRequestHandler({
    accessControl: kitchenAccessControl,
    restaurants,
    restaurantOrders,
    ids: { next: () => randomUUID() },
    clock,
  }),
  cancelRestaurantOrder: new CancelRestaurantOrderHandler({
    accessControl: kitchenAccessControl,
    unitOfWork,
    restaurantOrders,
    outbox,
    clock,
    // Open product decision; the conservative choice keeps Preparation going.
    cancellationDuringPreparation: "reject",
  }),
});
const server = createServer(
  connectNodeAdapter({
    routes: (router) => router.service(RestaurantsService, service),
  }),
);

try {
  await new Lifecycle({
    managementPort: config.management.port,
    readinessDelayMs: config.shutdown.readinessDelayMs,
    drainTimeoutMs: config.shutdown.drainTimeoutMs,
  }).run(new GrpcWorker(server, config.server.port), {
    log: (message) => console.log(`restaurants ${message}`),
  });
} catch (error) {
  console.error("restaurants stopped with error", error);
  process.exitCode = 1;
}
