// Composition root of `cmd/restaurants` (ADR 5): Awilix registrations of the
// shared clients, the slice modules and the gRPC worker. Every registration
// lives in the application scope: handlers are stateless over shared clients,
// and the per-operation resource — the MongoDB session of a transaction — is
// owned by `DatabaseUnitOfWork.run` for exactly one use case.
//
// Resources acquired asynchronously (MongoDB connection, subject resolver)
// are connected and released by cmd/restaurants and registered as values;
// the SpiceDB client is built by the container and closed by its disposer.
import { randomUUID } from "node:crypto";
import type { Http2Server } from "node:http2";

import { type AwilixContainer, aliasTo, asFunction, asValue } from "awilix";
import type { Valkey } from "iovalkey";
import type { Db, MongoClient } from "mongodb";

import { createGrpcServer } from "../infrastructure/grpc/server.ts";
import { createRestaurantsService } from "../infrastructure/grpc/service.ts";
import type { SubjectResolver } from "../infrastructure/grpc/subject.ts";
import { GrpcWorker } from "../infrastructure/grpc/worker.ts";
import { SpiceDbAccessControl } from "../infrastructure/spicedb/access-control.ts";
import { ObservedPermissionsClient } from "../infrastructure/spicedb/check-telemetry.ts";
import {
  createSpiceDbClient,
  type SpiceDbClient,
} from "../infrastructure/spicedb/client.ts";
import { SpiceDbRelationshipWriter } from "../infrastructure/spicedb/relationships.ts";
import type { Clock } from "../pkg/application/clock.ts";
import type { IdGenerator } from "../pkg/application/id-generator.ts";
import type { Outbox } from "../pkg/application/outbox.ts";
import type {
  KitchenConfig,
  ReconcilerConfig,
  ServerConfig,
  SimulatorConfig,
  SpiceDbEnabledConfig,
  ValkeyEnabledConfig,
} from "../pkg/config/config.ts";
import {
  createProcessContainer,
  type Registrations,
} from "../pkg/di/container.ts";
import { outboxEventMapperByAggregate } from "../pkg/infrastructure/outbox/integration-event.ts";
import type { DatabaseConnection } from "../pkg/infrastructure/repository/client.ts";
import {
  DatabaseOutbox,
  type OutboxEventMapper,
} from "../pkg/infrastructure/repository/outbox.ts";
import type { DatabaseUnitOfWork } from "../pkg/infrastructure/repository/unit-of-work.ts";
import { CacheMetrics } from "../pkg/infrastructure/valkey/read-cache.ts";
import type { Worker } from "../pkg/lifecycle/lifecycle.ts";
import { PeriodicLoop, WorkerWithLoops } from "../pkg/lifecycle/periodic.ts";
import type { Logger } from "../pkg/telemetry/logger.ts";
import { currentTraceMetadata } from "../pkg/telemetry/trace-context.ts";
import { UseCaseTelemetry } from "../pkg/telemetry/usecase.ts";
import type { MenuAccessControl } from "../slices/menu/applications/access-control.ts";
import {
  type MenuCradle,
  registerMenu,
} from "../slices/menu/di/registrations.ts";
import { MENU_AGGREGATE_TYPE } from "../slices/menu/infrastructure/integration/menu-events.ts";
import type {
  OwnerRelationshipWriter,
  RestaurantAccessControl,
  StaffRelationshipWriter,
} from "../slices/restaurant/applications/access-control.ts";
import {
  type RestaurantCradle,
  registerRestaurant,
} from "../slices/restaurant/di/registrations.ts";
import { RESTAURANT_AGGREGATE_TYPE } from "../slices/restaurant/infrastructure/integration/restaurant-events.ts";
import {
  type KitchenAccess,
  type RestaurantOrderCradle,
  registerRestaurantOrder,
} from "../slices/restaurantorder/di/registrations.ts";
import {
  type KitchenChangeNotifier,
  kitchenChangeNotifier,
} from "../slices/restaurantorder/infrastructure/integration/kitchen-changes.ts";
import { RESTAURANT_ORDER_AGGREGATE_TYPE } from "../slices/restaurantorder/infrastructure/integration/restaurant-order-events.ts";
import {
  ObservedDatabaseUnitOfWork,
  ObservedOutbox,
  RestaurantOrderDomainMetrics,
} from "../slices/restaurantorder/infrastructure/observability/domain-metrics.ts";
import type { SimulatorAccess } from "../slices/simulator/applications/access-control.ts";
import {
  registerSimulator,
  type SimulatorCradle,
} from "../slices/simulator/di/registrations.ts";
import { instrumentUseCases, RESTAURANTS_USE_CASES } from "./usecases.ts";

/** First reconciliation pass shortly after start. */
const RECONCILER_FIRST_PASS_MS = 10_000;

/** Configuration and resources the composition root hands to the container. */
export type RestaurantsResources = Readonly<{
  server: ServerConfig;
  spicedb: SpiceDbEnabledConfig;
  reconciler: ReconcilerConfig;
  kitchen: KitchenConfig;
  simulator: SimulatorConfig;
  database: DatabaseConnection;
  valkeyConfig: ValkeyEnabledConfig;
  /** Opened by cmd/restaurants; reads bypass the cache while it is down. */
  valkey: Valkey;
  subjects: SubjectResolver;
  logger: Logger;
}>;

/** Shared clients and ports of the process. */
export interface InfrastructureCradle {
  readonly serverConfig: ServerConfig;
  readonly spicedbConfig: SpiceDbEnabledConfig;
  readonly reconcilerConfig: ReconcilerConfig;
  readonly kitchenConfig: KitchenConfig;
  readonly simulatorConfig: SimulatorConfig;
  readonly databaseClient: MongoClient;
  readonly db: Db;
  readonly valkeyConfig: ValkeyEnabledConfig;
  readonly valkey: Valkey;
  readonly cacheMetrics: CacheMetrics;
  readonly clock: Clock;
  readonly ids: IdGenerator;
  /** Random number in `[0, 1)` of the Demo Simulator. */
  readonly random: () => number;
  /** Structured stdout logs (pkg/telemetry/logger.ts). */
  readonly logger: Logger;
  /** Span and duration of every use case listed in di/usecases.ts. */
  readonly useCaseTelemetry: UseCaseTelemetry;
  readonly domainMetrics: RestaurantOrderDomainMetrics;
  readonly unitOfWork: DatabaseUnitOfWork;
  /**
   * Outbox mapper of the process: dispatches by `aggregate_type` to the
   * allowlist of the owning slice; each aggregate has its own topic.
   */
  readonly outboxEventMapper: OutboxEventMapper;
  readonly outbox: Outbox;
  readonly kitchenChanges: KitchenChangeNotifier;
  readonly spicedb: SpiceDbClient;
  readonly spicedbAccessControl: SpiceDbAccessControl;
  readonly kitchenAccessControl: KitchenAccess;
  readonly restaurantAccessControl: RestaurantAccessControl;
  readonly menuAccessControl: MenuAccessControl;
  readonly simulatorAccessControl: SimulatorAccess;
  readonly relationshipWriter: SpiceDbRelationshipWriter;
  readonly ownerRelationships: OwnerRelationshipWriter;
  readonly staffRelationships: StaffRelationshipWriter;
}

/** Transport and background work of the process. */
export interface ProcessCradle {
  readonly subjects: SubjectResolver;
  readonly grpcServer: Http2Server;
  readonly reconcileLoop: PeriodicLoop;
  /** Demo Simulator of the kitchen (FR-605). */
  readonly simulatorLoop: PeriodicLoop;
  /** gRPC worker with the reconciliation and Simulator loops, run by the Lifecycle. */
  readonly worker: Worker;
}

export type RestaurantsCradle = InfrastructureCradle &
  RestaurantCradle &
  MenuCradle &
  RestaurantOrderCradle &
  SimulatorCradle &
  ProcessCradle;

export const createRestaurantsContainer = (
  resources: RestaurantsResources,
): AwilixContainer<RestaurantsCradle> => {
  const container = createProcessContainer<RestaurantsCradle>();
  container.register(infrastructureRegistrations(resources));
  registerRestaurant(container);
  registerMenu(container);
  registerRestaurantOrder(container);
  registerSimulator(container);
  instrumentUseCases(container, RESTAURANTS_USE_CASES);
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
  simulatorConfig: asValue(resources.simulator),
  databaseClient: asValue(resources.database.client),
  db: asValue(resources.database.db),
  valkeyConfig: asValue(resources.valkeyConfig),
  valkey: asValue(resources.valkey),
  cacheMetrics: asFunction(() => new CacheMetrics()).singleton(),
  clock: asValue<Clock>({ now: () => new Date() }),
  ids: asValue<IdGenerator>({ next: () => randomUUID() }),
  random: asValue(Math.random),
  logger: asValue(resources.logger),
  useCaseTelemetry: asFunction(() => new UseCaseTelemetry()).singleton(),
  // Business metrics change only after the outermost transaction commits, by
  // the Domain Events written to the Outbox (docs/observability.md#метрики).
  domainMetrics: asFunction(
    () => new RestaurantOrderDomainMetrics(),
  ).singleton(),
  unitOfWork: asFunction(
    ({ databaseClient, domainMetrics }: RestaurantsCradle) =>
      new ObservedDatabaseUnitOfWork(databaseClient, domainMetrics),
  ).singleton(),
  outboxEventMapper: asFunction(
    ({
      restaurantOutboxEventMapper,
      menuOutboxEventMapper,
      restaurantOrderOutboxEventMapper,
    }: RestaurantsCradle) =>
      outboxEventMapperByAggregate({
        [RESTAURANT_AGGREGATE_TYPE]: restaurantOutboxEventMapper,
        [MENU_AGGREGATE_TYPE]: menuOutboxEventMapper,
        [RESTAURANT_ORDER_AGGREGATE_TYPE]: restaurantOrderOutboxEventMapper,
      }),
  ).singleton(),
  outbox: asFunction(
    ({ db, unitOfWork, outboxEventMapper, domainMetrics }: RestaurantsCradle) =>
      new ObservedOutbox(
        new DatabaseOutbox(db, unitOfWork, {
          mapper: outboxEventMapper,
          metadata: currentTraceMetadata,
        }),
        domainMetrics,
      ),
  ).singleton(),
  // ADR 36: Change Notifications of the kitchen board, in the Outbox of the
  // same Unit of Work as the Restaurant or Restaurant Order write.
  kitchenChanges: asFunction(({ db, unitOfWork, clock }: RestaurantsCradle) =>
    kitchenChangeNotifier(
      unitOfWork,
      new DatabaseOutbox(db, unitOfWork, { metadata: currentTraceMetadata }),
      () => clock.now(),
    ),
  ).singleton(),
  // SpiceDB (ADR 18): one client for checks and writes; errors and timeouts
  // fail closed as UNAVAILABLE.
  spicedb: asFunction(({ spicedbConfig }: RestaurantsCradle) =>
    createSpiceDbClient(spicedbConfig),
  )
    .singleton()
    .disposer((client) => client.close()),
  // Checks of `restaurant:<id>` read the stored ZedToken from the same
  // repository the use cases save it to (at_least_as_fresh, FR-447). Every
  // check records nomnom.authz.checks and nomnom.authz.check.duration.
  spicedbAccessControl: asFunction(
    ({ spicedb, restaurants, spicedbConfig }: RestaurantsCradle) =>
      new SpiceDbAccessControl({
        client: new ObservedPermissionsClient(spicedb),
        revisions: restaurants,
        timeoutMs: spicedbConfig.timeoutMs,
      }),
  ).singleton(),
  kitchenAccessControl: aliasTo("spicedbAccessControl"),
  restaurantAccessControl: aliasTo("spicedbAccessControl"),
  menuAccessControl: aliasTo("spicedbAccessControl"),
  simulatorAccessControl: aliasTo("spicedbAccessControl"),
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
      getMenu: cradle.getMenu,
      listRestaurants: cradle.listRestaurants,
      listOwnedRestaurants: cradle.listOwnedRestaurants,
      receiveOrderRequest: cradle.receiveOrderRequest,
      cancelRestaurantOrder: cradle.cancelRestaurantOrder,
      acceptRestaurantOrder: cradle.acceptRestaurantOrder,
      rejectRestaurantOrder: cradle.rejectRestaurantOrder,
      startPreparation: cradle.startPreparation,
      markRestaurantOrderAssembled: cradle.markRestaurantOrderAssembled,
      markRestaurantOrderReady: cradle.markRestaurantOrderReady,
      reportPreparationDelay: cradle.reportPreparationDelay,
      reportPreparationFailure: cradle.reportPreparationFailure,
      getKitchenQueue: cradle.getKitchenQueue,
      listIncomingOrders: cradle.listIncomingOrders,
      listActiveRestaurantOrders: cradle.listActiveRestaurantOrders,
      registerRestaurant: cradle.registerRestaurant,
      assignStaff: cradle.assignStaff,
      revokeStaff: cradle.revokeStaff,
      openRestaurant: cradle.openRestaurant,
      closeRestaurant: cradle.closeRestaurant,
      createMenu: cradle.createMenu,
      addMenuItem: cradle.addMenuItem,
      updateMenuItem: cradle.updateMenuItem,
      setMenuItemAvailability: cradle.setMenuItemAvailability,
      getSimulator: cradle.getSimulator,
      setSimulatorEnabled: cradle.setSimulatorEnabled,
    });
    // Server spans continue the caller's traceparent (tracingInterceptor).
    return createGrpcServer(service, cradle.logger);
  }).singleton(),
  // Reconciliation (FR-445) runs in this process and stops before the gRPC
  // drain.
  reconcileLoop: asFunction(
    ({ relationshipReconciler, reconcilerConfig, logger }: RestaurantsCradle) =>
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
          log: logger.child({ operation: "reconcile" }).error,
        },
      ),
  ).singleton(),
  // Demo Simulator (FR-605): a disabled Simulator only reads its state.
  simulatorLoop: asFunction(
    ({ kitchenSimulator, simulatorConfig, logger }: RestaurantsCradle) =>
      new PeriodicLoop((signal) => kitchenSimulator.tick(signal), {
        initialDelayMs: simulatorConfig.intervalMs,
        intervalMs: simulatorConfig.intervalMs,
        log: logger.child({ operation: "simulator" }).error,
      }),
  ).singleton(),
  worker: asFunction(
    ({
      grpcServer,
      serverConfig,
      reconcileLoop,
      simulatorLoop,
    }: RestaurantsCradle) =>
      new WorkerWithLoops(new GrpcWorker(grpcServer, serverConfig.port), [
        reconcileLoop,
        simulatorLoop,
      ]),
  ).singleton(),
});
