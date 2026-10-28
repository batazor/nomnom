// Awilix registrations of the `restaurant` slice (ADR 5): MongoDB adapters,
// the Outbox mapper of the Restaurant aggregate, handlers exposed through
// transport and the relationship reconciler.
import { type AwilixContainer, asFunction, asValue } from "awilix";
import type { Valkey } from "iovalkey";
import type { Db } from "mongodb";

import type { RelationshipsClient } from "../../../infrastructure/spicedb/client.ts";
import type { Clock } from "../../../pkg/application/clock.ts";
import type { IdGenerator } from "../../../pkg/application/id-generator.ts";
import type { Outbox } from "../../../pkg/application/outbox.ts";
import type {
  KitchenConfig,
  ReconcilerConfig,
  SpiceDbConfig,
  ValkeyConfig,
  WaitTimeConfig,
} from "../../../pkg/config/config.ts";
import type { Registrations } from "../../../pkg/di/container.ts";
import type { OutboxEventMapper } from "../../../pkg/infrastructure/repository/outbox.ts";
import type { DatabaseUnitOfWork } from "../../../pkg/infrastructure/repository/unit-of-work.ts";
import type { VersionedCollection } from "../../../pkg/infrastructure/repository/versioned-collection.ts";
import type { CacheMetrics } from "../../../pkg/infrastructure/valkey/read-cache.ts";
import type { Logger } from "../../../pkg/telemetry/logger.ts";
import type {
  OwnerRelationshipWriter,
  RestaurantAccessControl,
  StaffRelationshipWriter,
} from "../applications/access-control.ts";
import { AssignStaffHandler } from "../applications/assign-staff/assign-staff.ts";
import { BatchGetRestaurantsHandler } from "../applications/batch-get-restaurants/batch-get-restaurants.ts";
import { CloseRestaurantHandler } from "../applications/close-restaurant/close-restaurant.ts";
import { GetRestaurantHandler } from "../applications/get-restaurant/get-restaurant.ts";
import { ListOwnedRestaurantsHandler } from "../applications/list-owned-restaurants/list-owned-restaurants.ts";
import { ListRestaurantsHandler } from "../applications/list-restaurants/list-restaurants.ts";
import { OpenRestaurantHandler } from "../applications/open-restaurant/open-restaurant.ts";
import { RegisterRestaurantHandler } from "../applications/register-restaurant/register-restaurant.ts";
import { RevokeStaffHandler } from "../applications/revoke-staff/revoke-staff.ts";
import { SetOpeningHoursHandler } from "../applications/set-opening-hours/set-opening-hours.ts";
import { SetScheduledOrderSettingsHandler } from "../applications/set-scheduled-order-settings/set-scheduled-order-settings.ts";
import { UpdateRestaurantHandler } from "../applications/update-restaurant/update-restaurant.ts";
import { WaitTimePolicy } from "../domains/restaurant/wait-time.ts";
import { CachedRestaurantCatalog } from "../infrastructure/cache/restaurants.ts";
import { restaurantEventMapper } from "../infrastructure/integration/restaurant-events.ts";
import { RelationshipReconciler } from "../infrastructure/reconcile/reconcile.ts";
import {
  createRestaurantCollection,
  DatabaseRestaurantCatalog,
  DatabaseRestaurantMemberships,
  DatabaseRestaurantRepository,
  DatabaseRestaurantWaitTimes,
  type RestaurantDocument,
  type RestaurantSaved,
} from "../infrastructure/repository/restaurants.ts";

/** Application-scope dependencies the slice takes from the process. */
export interface RestaurantDependencies {
  readonly db: Db;
  readonly unitOfWork: DatabaseUnitOfWork;
  readonly clock: Clock;
  readonly ids: IdGenerator;
  readonly outbox: Outbox;
  /** ADR 36: the kitchen board shows the restaurant, so every save notifies it. */
  readonly kitchenChanges: RestaurantSaved;
  /** ADR 43: Get Restaurant and List Restaurants are cached for `valkeyConfig.ttlMs`. */
  readonly valkey: Valkey;
  readonly valkeyConfig: ValkeyConfig;
  readonly cacheMetrics: CacheMetrics;
  readonly restaurantAccessControl: RestaurantAccessControl;
  readonly ownerRelationships: OwnerRelationshipWriter;
  readonly staffRelationships: StaffRelationshipWriter;
  readonly spicedb: RelationshipsClient;
  readonly spicedbConfig: SpiceDbConfig;
  readonly reconcilerConfig: ReconcilerConfig;
  readonly kitchenConfig: KitchenConfig;
  readonly waitTimeConfig: WaitTimeConfig;
  readonly logger: Logger;
}

/** What the slice provides to the process and to other slices. */
export interface RestaurantCradle {
  /**
   * Only `RestaurantOpened`/`RestaurantClosed` of the Restaurant aggregate are
   * written to the Outbox (explicit allowlist, topic
   * restaurants.v1.restaurant-events).
   */
  readonly restaurantOutboxEventMapper: OutboxEventMapper;
  /** One collection shared by the repository, memberships and catalog. */
  readonly restaurantCollection: VersionedCollection<RestaurantDocument>;
  /**
   * Also the stored revisions of `restaurant:<id>` read by the SpiceDB
   * access control (FR-447), hence the concrete type.
   */
  readonly restaurants: DatabaseRestaurantRepository;
  readonly restaurantMemberships: DatabaseRestaurantMemberships;
  /** Baseline, current wait and level of the Wait Time Statistics (FR-645). */
  readonly waitTimePolicy: WaitTimePolicy;
  /** Wait Time Statistics kept in the restaurant documents (FR-644). */
  readonly restaurantWaitTimes: DatabaseRestaurantWaitTimes;
  readonly restaurantCatalog: DatabaseRestaurantCatalog;
  /** Get, Batch Get and List Restaurants through the Valkey read cache (ADR 43). */
  readonly cachedRestaurantCatalog: CachedRestaurantCatalog;
  readonly getRestaurant: GetRestaurantHandler;
  readonly batchGetRestaurants: BatchGetRestaurantsHandler;
  readonly listRestaurants: ListRestaurantsHandler;
  readonly listOwnedRestaurants: ListOwnedRestaurantsHandler;
  readonly registerRestaurant: RegisterRestaurantHandler;
  readonly updateRestaurant: UpdateRestaurantHandler;
  readonly setOpeningHours: SetOpeningHoursHandler;
  readonly setScheduledOrderSettings: SetScheduledOrderSettingsHandler;
  readonly assignStaff: AssignStaffHandler;
  readonly revokeStaff: RevokeStaffHandler;
  readonly openRestaurant: OpenRestaurantHandler;
  readonly closeRestaurant: CloseRestaurantHandler;
  readonly relationshipReconciler: RelationshipReconciler;
}

type Cradle = RestaurantDependencies & RestaurantCradle;

export const registerRestaurant = (
  container: AwilixContainer<Cradle>,
): void => {
  const registrations: Registrations<RestaurantCradle> = {
    restaurantOutboxEventMapper: asValue(restaurantEventMapper),
    restaurantCollection: asFunction(({ db, unitOfWork }: Cradle) =>
      createRestaurantCollection(db, unitOfWork),
    ).singleton(),
    restaurants: asFunction(
      ({ restaurantCollection, kitchenChanges }: Cradle) =>
        new DatabaseRestaurantRepository(restaurantCollection, kitchenChanges),
    ).singleton(),
    restaurantMemberships: asFunction(
      ({ restaurantCollection }: Cradle) =>
        new DatabaseRestaurantMemberships(restaurantCollection),
    ).singleton(),
    waitTimePolicy: asFunction(
      ({ waitTimeConfig, kitchenConfig }: Cradle) =>
        new WaitTimePolicy({
          ...waitTimeConfig,
          preparationTimeMs: kitchenConfig.preparationTimeMs,
        }),
    ).singleton(),
    restaurantWaitTimes: asFunction(
      ({ restaurantCollection }: Cradle) =>
        new DatabaseRestaurantWaitTimes(restaurantCollection),
    ).singleton(),
    restaurantCatalog: asFunction(
      ({ restaurantCollection, waitTimePolicy, clock }: Cradle) =>
        new DatabaseRestaurantCatalog(restaurantCollection, {
          policy: waitTimePolicy,
          now: () => clock.now(),
        }),
    ).singleton(),
    cachedRestaurantCatalog: asFunction(
      ({ restaurantCatalog, valkey, valkeyConfig, cacheMetrics }: Cradle) =>
        new CachedRestaurantCatalog(restaurantCatalog, {
          client: valkey,
          ttlMs: valkeyConfig.ttlMs,
          metrics: cacheMetrics,
        }),
    ).singleton(),
    getRestaurant: asFunction(
      ({ cachedRestaurantCatalog, clock }: Cradle) =>
        new GetRestaurantHandler({ reader: cachedRestaurantCatalog, clock }),
    ).singleton(),
    batchGetRestaurants: asFunction(
      ({ cachedRestaurantCatalog, clock }: Cradle) =>
        new BatchGetRestaurantsHandler({
          reader: cachedRestaurantCatalog,
          clock,
        }),
    ).singleton(),
    listRestaurants: asFunction(
      ({ cachedRestaurantCatalog, clock }: Cradle) =>
        new ListRestaurantsHandler({ reader: cachedRestaurantCatalog, clock }),
    ).singleton(),
    listOwnedRestaurants: asFunction(
      ({ restaurantCatalog }: Cradle) =>
        new ListOwnedRestaurantsHandler({ reader: restaurantCatalog }),
    ).singleton(),
    registerRestaurant: asFunction(
      ({
        restaurantAccessControl,
        ownerRelationships,
        restaurants,
        ids,
        clock,
      }: Cradle) =>
        new RegisterRestaurantHandler({
          accessControl: restaurantAccessControl,
          ownerRelationships,
          restaurants,
          ids,
          clock,
        }),
    ).singleton(),
    updateRestaurant: asFunction(
      ({ restaurantAccessControl, restaurants, clock }: Cradle) =>
        new UpdateRestaurantHandler({
          accessControl: restaurantAccessControl,
          restaurants,
          clock,
        }),
    ).singleton(),
    setOpeningHours: asFunction(
      ({ restaurantAccessControl, restaurants }: Cradle) =>
        new SetOpeningHoursHandler({
          accessControl: restaurantAccessControl,
          restaurants,
        }),
    ).singleton(),
    setScheduledOrderSettings: asFunction(
      ({ restaurantAccessControl, restaurants }: Cradle) =>
        new SetScheduledOrderSettingsHandler({
          accessControl: restaurantAccessControl,
          restaurants,
        }),
    ).singleton(),
    assignStaff: asFunction(
      ({ restaurantAccessControl, staffRelationships, restaurants }: Cradle) =>
        new AssignStaffHandler({
          accessControl: restaurantAccessControl,
          staffRelationships,
          restaurants,
        }),
    ).singleton(),
    revokeStaff: asFunction(
      ({ restaurantAccessControl, staffRelationships, restaurants }: Cradle) =>
        new RevokeStaffHandler({
          accessControl: restaurantAccessControl,
          staffRelationships,
          restaurants,
        }),
    ).singleton(),
    // Open/Close Restaurant write RestaurantOpened/RestaurantClosed to the
    // Outbox in the Unit of Work of the opening change (FR-203).
    openRestaurant: asFunction(
      ({
        restaurantAccessControl,
        unitOfWork,
        restaurants,
        outbox,
        clock,
      }: Cradle) =>
        new OpenRestaurantHandler({
          accessControl: restaurantAccessControl,
          unitOfWork,
          restaurants,
          outbox,
          clock,
        }),
    ).singleton(),
    closeRestaurant: asFunction(
      ({
        restaurantAccessControl,
        unitOfWork,
        restaurants,
        outbox,
        clock,
      }: Cradle) =>
        new CloseRestaurantHandler({
          accessControl: restaurantAccessControl,
          unitOfWork,
          restaurants,
          outbox,
          clock,
        }),
    ).singleton(),
    // Repairs owner/staff relationships of the dual-write (FR-445).
    relationshipReconciler: asFunction(
      ({
        spicedb,
        restaurantMemberships,
        reconcilerConfig,
        spicedbConfig,
        clock,
        logger,
      }: Cradle) =>
        new RelationshipReconciler({
          client: spicedb,
          restaurants: restaurantMemberships,
          graceMs: reconcilerConfig.graceMs,
          pageSize: reconcilerConfig.pageSize,
          timeoutMs: spicedbConfig.timeoutMs,
          clock,
          log: logger.child({ operation: "reconcile" }).info,
        }),
    ).singleton(),
  };
  container.register(registrations);
};
