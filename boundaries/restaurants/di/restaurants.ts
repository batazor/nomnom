// Composition root of `cmd/restaurants` (ADR 5): Awilix registrations of the
// shared clients, the slice modules and the gRPC worker. Every registration
// lives in the application scope: handlers are stateless over shared clients,
// and the per-operation resource — the MongoDB session of a transaction — is
// owned by `MongoUnitOfWork.run` for exactly one use case.
//
// Resources acquired asynchronously (MongoDB connection, subject resolver)
// are connected and released by cmd/restaurants and registered as values;
// the SpiceDB client is built by the container and closed by its disposer.
import { randomUUID } from "node:crypto";
import { createServer, type Http2Server } from "node:http2";

import { connectNodeAdapter } from "@connectrpc/connect-node";
import { type AwilixContainer, aliasTo, asFunction, asValue } from "awilix";
import type { Db, MongoClient } from "mongodb";

import { RestaurantsService } from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { createRestaurantsService } from "../infrastructure/grpc/service.ts";
import type { SubjectResolver } from "../infrastructure/grpc/subject.ts";
import { GrpcWorker } from "../infrastructure/grpc/worker.ts";
import {
  createSpiceDbClient,
  SpiceDbAccessControl,
  type SpiceDbClient,
} from "../infrastructure/spicedb/access-control.ts";
import { SpiceDbRelationshipWriter } from "../infrastructure/spicedb/relationships.ts";
import type { Clock } from "../pkg/application/clock.ts";
import type { IdGenerator } from "../pkg/application/id-generator.ts";
import type { Outbox } from "../pkg/application/outbox.ts";
import type {
  KitchenConfig,
  ReconcilerConfig,
  ServerConfig,
  SpiceDbEnabledConfig,
} from "../pkg/config/config.ts";
import {
  createProcessContainer,
  type Registrations,
} from "../pkg/di/container.ts";
import type { MongoDbConnection } from "../pkg/infrastructure/mongodb/client.ts";
import { MongoOutbox } from "../pkg/infrastructure/mongodb/outbox.ts";
import { MongoUnitOfWork } from "../pkg/infrastructure/mongodb/unit-of-work.ts";
import type { Worker } from "../pkg/lifecycle/lifecycle.ts";
import { PeriodicLoop, WorkerWithLoops } from "../pkg/lifecycle/periodic.ts";
import { currentTraceMetadata } from "../pkg/telemetry/trace-context.ts";
import type { KitchenAccessControl } from "../slices/kitchenorder/applications/access-control.ts";
import {
  type KitchenOrderCradle,
  registerKitchenOrder,
} from "../slices/kitchenorder/di/registrations.ts";
import {
  type MenuCradle,
  registerMenu,
} from "../slices/menu/di/registrations.ts";
import type {
  OwnerRelationshipWriter,
  RestaurantAccessControl,
  StaffRelationshipWriter,
} from "../slices/restaurant/applications/access-control.ts";
import {
  type RestaurantCradle,
  registerRestaurant,
} from "../slices/restaurant/di/registrations.ts";

/** First reconciliation pass shortly after start. */
const RECONCILER_FIRST_PASS_MS = 10_000;

/** Configuration and resources the composition root hands to the container. */
export type RestaurantsResources = Readonly<{
  server: ServerConfig;
  spicedb: SpiceDbEnabledConfig;
  reconciler: ReconcilerConfig;
  kitchen: KitchenConfig;
  mongo: MongoDbConnection;
  subjects: SubjectResolver;
}>;

/** Shared clients and ports of the process. */
export interface InfrastructureCradle {
  readonly serverConfig: ServerConfig;
  readonly spicedbConfig: SpiceDbEnabledConfig;
  readonly reconcilerConfig: ReconcilerConfig;
  readonly kitchenConfig: KitchenConfig;
  readonly mongoClient: MongoClient;
  readonly db: Db;
  readonly clock: Clock;
  readonly ids: IdGenerator;
  readonly unitOfWork: MongoUnitOfWork;
  readonly outbox: Outbox;
  readonly spicedb: SpiceDbClient;
  readonly spicedbAccessControl: SpiceDbAccessControl;
  readonly kitchenAccessControl: KitchenAccessControl;
  readonly restaurantAccessControl: RestaurantAccessControl;
  readonly relationshipWriter: SpiceDbRelationshipWriter;
  readonly ownerRelationships: OwnerRelationshipWriter;
  readonly staffRelationships: StaffRelationshipWriter;
}

/** Transport and background work of the process. */
export interface ProcessCradle {
  readonly subjects: SubjectResolver;
  readonly grpcServer: Http2Server;
  readonly reconcileLoop: PeriodicLoop;
  /** gRPC worker with the reconciliation loop, run by the Lifecycle. */
  readonly worker: Worker;
}

export type RestaurantsCradle = InfrastructureCradle &
  RestaurantCradle &
  MenuCradle &
  KitchenOrderCradle &
  ProcessCradle;

export const createRestaurantsContainer = (
  resources: RestaurantsResources,
): AwilixContainer<RestaurantsCradle> => {
  const container = createProcessContainer<RestaurantsCradle>();
  container.register(infrastructureRegistrations(resources));
  registerRestaurant(container);
  registerMenu(container);
  registerKitchenOrder(container);
  container.register(processRegistrations(resources));
  return container;
};

const infrastructureRegistrations = (
  resources: RestaurantsResources,
): Registrations<InfrastructureCradle> => ({
  serverConfig: asValue(resources.server),
  spicedbConfig: asValue(resources.spicedb),
  reconcilerConfig: asValue(resources.reconciler),
  kitchenConfig: asValue(resources.kitchen),
  mongoClient: asValue(resources.mongo.client),
  db: asValue(resources.mongo.db),
  clock: asValue<Clock>({ now: () => new Date() }),
  ids: asValue<IdGenerator>({ next: () => randomUUID() }),
  unitOfWork: asFunction(
    ({ mongoClient }: RestaurantsCradle) => new MongoUnitOfWork(mongoClient),
  ).singleton(),
  outbox: asFunction(
    ({ db, unitOfWork, outboxEventMapper }: RestaurantsCradle) =>
      new MongoOutbox(db, unitOfWork, {
        mapper: outboxEventMapper,
        metadata: currentTraceMetadata,
      }),
  ).singleton(),
  // SpiceDB (ADR 18): one client for checks and writes; errors and timeouts
  // fail closed as UNAVAILABLE.
  spicedb: asFunction(({ spicedbConfig }: RestaurantsCradle) =>
    createSpiceDbClient(spicedbConfig),
  )
    .singleton()
    .disposer((client) => client.close()),
  // Checks of `restaurant:<id>` read the stored ZedToken from the same
  // repository the use cases save it to (at_least_as_fresh, FR-447).
  spicedbAccessControl: asFunction(
    ({ spicedb, restaurants, spicedbConfig }: RestaurantsCradle) =>
      new SpiceDbAccessControl({
        client: spicedb,
        revisions: restaurants,
        timeoutMs: spicedbConfig.timeoutMs,
      }),
  ).singleton(),
  kitchenAccessControl: aliasTo("spicedbAccessControl"),
  restaurantAccessControl: aliasTo("spicedbAccessControl"),
  // Owner/staff relationships are written before the restaurant is saved
  // (dual-write, repaired by the reconciler).
  relationshipWriter: asFunction(
    ({ spicedb, spicedbConfig }: RestaurantsCradle) =>
      new SpiceDbRelationshipWriter({
        client: spicedb,
        timeoutMs: spicedbConfig.timeoutMs,
      }),
  ).singleton(),
  ownerRelationships: aliasTo("relationshipWriter"),
  staffRelationships: aliasTo("relationshipWriter"),
});

const processRegistrations = (
  resources: RestaurantsResources,
): Registrations<ProcessCradle> => ({
  subjects: asValue(resources.subjects),
  grpcServer: asFunction((cradle: RestaurantsCradle) => {
    const service = createRestaurantsService({
      subjects: cradle.subjects,
      getRestaurant: cradle.getRestaurant,
      receiveOrderRequest: cradle.receiveOrderRequest,
      cancelRestaurantOrder: cradle.cancelRestaurantOrder,
      acceptRestaurantOrder: cradle.acceptRestaurantOrder,
      rejectRestaurantOrder: cradle.rejectRestaurantOrder,
      startPreparation: cradle.startPreparation,
      markRestaurantOrderAssembled: cradle.markRestaurantOrderAssembled,
      markRestaurantOrderReady: cradle.markRestaurantOrderReady,
      reportPreparationDelay: cradle.reportPreparationDelay,
      reportPreparationFailure: cradle.reportPreparationFailure,
      registerRestaurant: cradle.registerRestaurant,
      assignStaff: cradle.assignStaff,
      revokeStaff: cradle.revokeStaff,
    });
    return createServer(
      connectNodeAdapter({
        routes: (router) => router.service(RestaurantsService, service),
      }),
    );
  }).singleton(),
  // Reconciliation (FR-445) runs in this process and stops before the gRPC
  // drain.
  reconcileLoop: asFunction(
    ({ relationshipReconciler, reconcilerConfig }: RestaurantsCradle) =>
      new PeriodicLoop(
        async (signal) => {
          await relationshipReconciler.reconcile(signal);
        },
        {
          initialDelayMs: Math.min(
            RECONCILER_FIRST_PASS_MS,
            reconcilerConfig.intervalMs,
          ),
          intervalMs: reconcilerConfig.intervalMs,
          log: (message) => console.error(`restaurants reconcile ${message}`),
        },
      ),
  ).singleton(),
  worker: asFunction(
    ({ grpcServer, serverConfig, reconcileLoop }: RestaurantsCradle) =>
      new WorkerWithLoops(new GrpcWorker(grpcServer, serverConfig.port), [
        reconcileLoop,
      ]),
  ).singleton(),
});
