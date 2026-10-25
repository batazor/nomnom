import { randomUUID } from "node:crypto";
import { createServer } from "node:http2";

import { connectNodeAdapter } from "@connectrpc/connect-node";

import { RestaurantsService } from "../../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { createRestaurantsService } from "../../infrastructure/grpc/service.ts";
import { createSubjectResolver } from "../../infrastructure/grpc/subject.ts";
import { GrpcWorker } from "../../infrastructure/grpc/worker.ts";
import {
  createSpiceDbClient,
  SpiceDbAccessControl,
  type SpiceDbClient,
} from "../../infrastructure/spicedb/access-control.ts";
import { SpiceDbRelationshipWriter } from "../../infrastructure/spicedb/relationships.ts";
import { loadConfig, loadLocalEnv } from "../../pkg/config/config.ts";
import { InMemoryOutbox } from "../../pkg/infrastructure/memory/outbox.ts";
import { InMemoryUnitOfWork } from "../../pkg/infrastructure/memory/unit-of-work.ts";
import { Lifecycle } from "../../pkg/lifecycle/lifecycle.ts";
import { PeriodicLoop, WorkerWithLoops } from "../../pkg/lifecycle/periodic.ts";
import type { KitchenAccessControl } from "../../slices/kitchenorder/applications/access-control.ts";
import { CancelRestaurantOrderHandler } from "../../slices/kitchenorder/applications/cancel-restaurant-order/cancel-restaurant-order.ts";
import { ReceiveOrderRequestHandler } from "../../slices/kitchenorder/applications/receive-order-request/receive-order-request.ts";
import {
  createRestaurantOrderStore,
  InMemoryRestaurantOrderRepository,
} from "../../slices/kitchenorder/infrastructure/memory/restaurant-orders.ts";
import type {
  OwnerRelationshipWriter,
  RestaurantAccessControl,
  StaffRelationshipWriter,
} from "../../slices/restaurant/applications/access-control.ts";
import { AssignStaffHandler } from "../../slices/restaurant/applications/assign-staff/assign-staff.ts";
import { GetRestaurantHandler } from "../../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import { RegisterRestaurantHandler } from "../../slices/restaurant/applications/register-restaurant/register-restaurant.ts";
import { RevokeStaffHandler } from "../../slices/restaurant/applications/revoke-staff/revoke-staff.ts";
import {
  createRestaurantStore,
  InMemoryRestaurantCatalog,
  InMemoryRestaurantMemberships,
  InMemoryRestaurantRepository,
} from "../../slices/restaurant/infrastructure/memory/restaurants.ts";
import { RelationshipReconciler } from "../../slices/restaurant/infrastructure/reconcile/reconcile.ts";

/** First reconciliation pass shortly after start. */
const RECONCILER_FIRST_PASS_MS = 10_000;

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
// SpiceDB (ADR 18) is required; errors and timeouts fail closed as
// UNAVAILABLE. Without SPICEDB__ENDPOINT the process does not start.
let spicedb: SpiceDbClient | undefined;
let kitchenAccessControl: KitchenAccessControl;
let restaurantAccessControl: RestaurantAccessControl;
let relationshipWriter: OwnerRelationshipWriter & StaffRelationshipWriter;
let reconciler: RelationshipReconciler;
if (config.spicedb.enabled) {
  spicedb = createSpiceDbClient(config.spicedb);
  const accessControl = new SpiceDbAccessControl({
    client: spicedb,
    timeoutMs: config.spicedb.timeoutMs,
  });
  kitchenAccessControl = accessControl;
  restaurantAccessControl = accessControl;
  // One client for checks and writes; owner/staff relationships are written
  // before the restaurant is saved (dual-write, repaired by the reconciler).
  relationshipWriter = new SpiceDbRelationshipWriter({
    client: spicedb,
    timeoutMs: config.spicedb.timeoutMs,
  });
  reconciler = new RelationshipReconciler({
    client: spicedb,
    restaurants: new InMemoryRestaurantMemberships(restaurantStore),
    graceMs: config.reconciler.graceMs,
    pageSize: config.reconciler.pageSize,
    timeoutMs: config.spicedb.timeoutMs,
    clock,
    log: (message) => console.log(`restaurants ${message}`),
  });
} else {
  throw new Error(
    "SPICEDB__ENDPOINT is required: restaurants checks permissions in SpiceDB",
  );
}

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
  registerRestaurant: new RegisterRestaurantHandler({
    accessControl: restaurantAccessControl,
    ownerRelationships: relationshipWriter,
    restaurants,
    ids: { next: () => randomUUID() },
    clock,
  }),
  assignStaff: new AssignStaffHandler({
    accessControl: restaurantAccessControl,
    staffRelationships: relationshipWriter,
    restaurants,
  }),
  revokeStaff: new RevokeStaffHandler({
    accessControl: restaurantAccessControl,
    staffRelationships: relationshipWriter,
    restaurants,
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
  }).run(
    // Storage is in process memory, so reconciliation (FR-445) runs here and
    // stops before the gRPC drain.
    new WorkerWithLoops(new GrpcWorker(server, config.server.port), [
      new PeriodicLoop(
        async (signal) => {
          await reconciler.reconcile(signal);
        },
        {
          initialDelayMs: Math.min(
            RECONCILER_FIRST_PASS_MS,
            config.reconciler.intervalMs,
          ),
          intervalMs: config.reconciler.intervalMs,
          log: (message) => console.error(`restaurants reconcile ${message}`),
        },
      ),
    ]),
    {
      log: (message) => console.log(`restaurants ${message}`),
    },
  );
} catch (error) {
  console.error("restaurants stopped with error", error);
  process.exitCode = 1;
} finally {
  spicedb?.close();
}
