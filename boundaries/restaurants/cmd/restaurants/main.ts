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
import { connectMongoDb } from "../../pkg/infrastructure/mongodb/client.ts";
import { MongoOutbox } from "../../pkg/infrastructure/mongodb/outbox.ts";
import { MongoUnitOfWork } from "../../pkg/infrastructure/mongodb/unit-of-work.ts";
import { Lifecycle } from "../../pkg/lifecycle/lifecycle.ts";
import { PeriodicLoop, WorkerWithLoops } from "../../pkg/lifecycle/periodic.ts";
import { currentTraceMetadata } from "../../pkg/telemetry/trace-context.ts";
import { AcceptOrderHandler } from "../../slices/kitchenorder/applications/accept-order/accept-order.ts";
import type { KitchenAccessControl } from "../../slices/kitchenorder/applications/access-control.ts";
import { CancelRestaurantOrderHandler } from "../../slices/kitchenorder/applications/cancel-restaurant-order/cancel-restaurant-order.ts";
import { MarkAssembledHandler } from "../../slices/kitchenorder/applications/mark-assembled/mark-assembled.ts";
import { MarkReadyForPickupHandler } from "../../slices/kitchenorder/applications/mark-order-ready/mark-order-ready.ts";
import { ReceiveOrderRequestHandler } from "../../slices/kitchenorder/applications/receive-order-request/receive-order-request.ts";
import { RejectOrderHandler } from "../../slices/kitchenorder/applications/reject-order/reject-order.ts";
import { ReportPreparationDelayHandler } from "../../slices/kitchenorder/applications/report-preparation-delay/report-preparation-delay.ts";
import { ReportPreparationFailureHandler } from "../../slices/kitchenorder/applications/report-preparation-failure/report-preparation-failure.ts";
import type { RepeatPolicy } from "../../slices/kitchenorder/applications/restaurant-order-support.ts";
import { StartPreparationHandler } from "../../slices/kitchenorder/applications/start-preparing-order/start-preparing-order.ts";
import {
  FixedPreparationTimeEstimator,
  UnconstrainedAcceptanceConditions,
} from "../../slices/kitchenorder/infrastructure/acceptance/mvp.ts";
import { restaurantOrderEventMapper } from "../../slices/kitchenorder/infrastructure/integration/restaurant-order-events.ts";
import {
  createRestaurantOrderCollection,
  MongoRestaurantOrderRepository,
} from "../../slices/kitchenorder/infrastructure/mongodb/restaurant-orders.ts";
import {
  createMenuCollection,
  MongoMenuRepository,
} from "../../slices/menu/infrastructure/mongodb/menus.ts";
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
  createRestaurantCollection,
  MongoRestaurantCatalog,
  MongoRestaurantMemberships,
  MongoRestaurantRepository,
} from "../../slices/restaurant/infrastructure/mongodb/restaurants.ts";
import { RelationshipReconciler } from "../../slices/restaurant/infrastructure/reconcile/reconcile.ts";

/** First reconciliation pass shortly after start. */
const RECONCILER_FIRST_PASS_MS = 10_000;
/**
 * Initial `expected_ready_at` after Acceptance until the estimate is a
 * product decision; staff move it later with ReportPreparationDelay.
 */
const PREPARATION_TIME_MS = 20 * 60_000;
/**
 * Open product decision: a repeated Start Preparation or Mark Ready answers
 * with the current order and publishes nothing, so staff UI retries are safe.
 */
const KITCHEN_REPEAT_POLICY: RepeatPolicy = "succeed-without-event";

loadLocalEnv();
const config = loadConfig();
// Projected ServiceAccount token verification (ADR 27) and BFF user
// assertions (ADR 32). Without issuer, audience and JWKS the
// RejectingSubjectResolver keeps commands fail closed; an invalid JWKS or
// allowlist stops startup.
const subjects = await createSubjectResolver(
  config.identity,
  (message) => console.warn(`restaurants ${message}`),
  { audit: (message) => console.info(`restaurants ${message}`) },
);

// MongoDB (ADR 8, local ADR 1) is required; collections, validators and
// indexes are created by restaurants-migrate before the process starts.
if (!config.mongodb.enabled) {
  throw new Error(
    "MONGODB__URI is required: restaurants stores its data in MongoDB",
  );
}
const mongo = await connectMongoDb(config.mongodb);

const clock = { now: () => new Date() };
const unitOfWork = new MongoUnitOfWork(mongo.client);
// Only Kitchen Order facts with a contract in events.proto are written to the
// Outbox (explicit allowlist); restaurants-relay publishes them (ADR 31).
const outbox = new MongoOutbox(mongo.db, unitOfWork, {
  mapper: restaurantOrderEventMapper,
  metadata: currentTraceMetadata,
});
const restaurantCollection = createRestaurantCollection(mongo.db, unitOfWork);
const orderCollection = createRestaurantOrderCollection(mongo.db, unitOfWork);
const restaurants = new MongoRestaurantRepository(restaurantCollection);
const restaurantOrders = new MongoRestaurantOrderRepository(orderCollection);
const menus = new MongoMenuRepository(
  createMenuCollection(mongo.db, unitOfWork),
);
// SpiceDB (ADR 18) is required; errors and timeouts fail closed as
// UNAVAILABLE. Without SPICEDB__ENDPOINT the process does not start.
let spicedb: SpiceDbClient | undefined;
let kitchenAccessControl: KitchenAccessControl;
let restaurantAccessControl: RestaurantAccessControl;
let relationshipWriter: OwnerRelationshipWriter & StaffRelationshipWriter;
let reconciler: RelationshipReconciler;
if (config.spicedb.enabled) {
  spicedb = createSpiceDbClient(config.spicedb);
  // Checks of `restaurant:<id>` read the stored ZedToken from the same
  // repository the use cases save it to (at_least_as_fresh, FR-447).
  const accessControl = new SpiceDbAccessControl({
    client: spicedb,
    revisions: restaurants,
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
    restaurants: new MongoRestaurantMemberships(restaurantCollection),
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

// Kitchen commands of restaurant staff: one MongoDB transaction saves the
// Restaurant Order and its published facts to the Outbox.
const kitchenCommands = {
  accessControl: kitchenAccessControl,
  unitOfWork,
  restaurantOrders,
  outbox,
  clock,
};

const service = createRestaurantsService({
  subjects,
  getRestaurant: new GetRestaurantHandler({
    reader: new MongoRestaurantCatalog(restaurantCollection),
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
  acceptRestaurantOrder: new AcceptOrderHandler({
    ...kitchenCommands,
    restaurants,
    menus,
    conditions: new UnconstrainedAcceptanceConditions(),
    readyTimeEstimator: new FixedPreparationTimeEstimator(PREPARATION_TIME_MS),
  }),
  rejectRestaurantOrder: new RejectOrderHandler(kitchenCommands),
  startPreparation: new StartPreparationHandler({
    ...kitchenCommands,
    onRepeat: KITCHEN_REPEAT_POLICY,
  }),
  // Assembly is a local fact: a single document write without the Outbox.
  markRestaurantOrderAssembled: new MarkAssembledHandler({
    accessControl: kitchenAccessControl,
    restaurantOrders,
    clock,
  }),
  markRestaurantOrderReady: new MarkReadyForPickupHandler({
    ...kitchenCommands,
    onRepeat: KITCHEN_REPEAT_POLICY,
  }),
  reportPreparationDelay: new ReportPreparationDelayHandler(kitchenCommands),
  reportPreparationFailure: new ReportPreparationFailureHandler(
    kitchenCommands,
  ),
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
    // Reconciliation (FR-445) runs in this process and stops before the
    // gRPC drain.
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
  await mongo.close();
}
