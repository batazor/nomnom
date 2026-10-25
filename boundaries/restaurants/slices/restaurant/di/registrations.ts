// Awilix registrations of the `restaurant` slice (ADR 5): MongoDB adapters,
// the Outbox mapper of the Restaurant aggregate, handlers exposed through
// transport and the relationship reconciler.
import { type AwilixContainer, asFunction, asValue } from "awilix";
import type { Db } from "mongodb";

import type { RelationshipsClient } from "../../../infrastructure/spicedb/client.ts";
import type { Clock } from "../../../pkg/application/clock.ts";
import type { IdGenerator } from "../../../pkg/application/id-generator.ts";
import type { Outbox } from "../../../pkg/application/outbox.ts";
import type {
  ReconcilerConfig,
  SpiceDbEnabledConfig,
} from "../../../pkg/config/config.ts";
import type { Registrations } from "../../../pkg/di/container.ts";
import type { OutboxEventMapper } from "../../../pkg/infrastructure/mongodb/outbox.ts";
import type { MongoUnitOfWork } from "../../../pkg/infrastructure/mongodb/unit-of-work.ts";
import type { VersionedCollection } from "../../../pkg/infrastructure/mongodb/versioned-collection.ts";
import type { Logger } from "../../../pkg/telemetry/logger.ts";
import type {
  OwnerRelationshipWriter,
  RestaurantAccessControl,
  StaffRelationshipWriter,
} from "../applications/access-control.ts";
import { AssignStaffHandler } from "../applications/assign-staff/assign-staff.ts";
import { CloseRestaurantHandler } from "../applications/close-restaurant/close-restaurant.ts";
import { GetRestaurantHandler } from "../applications/get-restaurant/get-restaurant.ts";
import { ListRestaurantsHandler } from "../applications/list-restaurants/list-restaurants.ts";
import { OpenRestaurantHandler } from "../applications/open-restaurant/open-restaurant.ts";
import { RegisterRestaurantHandler } from "../applications/register-restaurant/register-restaurant.ts";
import { RevokeStaffHandler } from "../applications/revoke-staff/revoke-staff.ts";
import { restaurantEventMapper } from "../infrastructure/integration/restaurant-events.ts";
import {
  createRestaurantCollection,
  MongoRestaurantCatalog,
  MongoRestaurantMemberships,
  MongoRestaurantRepository,
  type RestaurantDocument,
} from "../infrastructure/mongodb/restaurants.ts";
import { RelationshipReconciler } from "../infrastructure/reconcile/reconcile.ts";

/** Application-scope dependencies the slice takes from the process. */
export interface RestaurantDependencies {
  readonly db: Db;
  readonly unitOfWork: MongoUnitOfWork;
  readonly clock: Clock;
  readonly ids: IdGenerator;
  readonly outbox: Outbox;
  readonly restaurantAccessControl: RestaurantAccessControl;
  readonly ownerRelationships: OwnerRelationshipWriter;
  readonly staffRelationships: StaffRelationshipWriter;
  readonly spicedb: RelationshipsClient;
  readonly spicedbConfig: SpiceDbEnabledConfig;
  readonly reconcilerConfig: ReconcilerConfig;
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
  readonly restaurants: MongoRestaurantRepository;
  readonly restaurantMemberships: MongoRestaurantMemberships;
  readonly restaurantCatalog: MongoRestaurantCatalog;
  readonly getRestaurant: GetRestaurantHandler;
  readonly listRestaurants: ListRestaurantsHandler;
  readonly registerRestaurant: RegisterRestaurantHandler;
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
      ({ restaurantCollection }: Cradle) =>
        new MongoRestaurantRepository(restaurantCollection),
    ).singleton(),
    restaurantMemberships: asFunction(
      ({ restaurantCollection }: Cradle) =>
        new MongoRestaurantMemberships(restaurantCollection),
    ).singleton(),
    restaurantCatalog: asFunction(
      ({ restaurantCollection }: Cradle) =>
        new MongoRestaurantCatalog(restaurantCollection),
    ).singleton(),
    getRestaurant: asFunction(
      ({ restaurantCatalog }: Cradle) =>
        new GetRestaurantHandler({ reader: restaurantCatalog }),
    ).singleton(),
    listRestaurants: asFunction(
      ({ restaurantCatalog }: Cradle) =>
        new ListRestaurantsHandler({ reader: restaurantCatalog }),
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
