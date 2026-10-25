// Awilix registrations of the `restaurant` slice (ADR 5): MongoDB adapters,
// handlers exposed through transport and the relationship reconciler.
import { type AwilixContainer, asFunction } from "awilix";
import type { Db } from "mongodb";

import type { RelationshipsClient } from "../../../infrastructure/spicedb/access-control.ts";
import type { Clock } from "../../../pkg/application/clock.ts";
import type { IdGenerator } from "../../../pkg/application/id-generator.ts";
import type {
  ReconcilerConfig,
  SpiceDbEnabledConfig,
} from "../../../pkg/config/config.ts";
import type { Registrations } from "../../../pkg/di/container.ts";
import type { MongoUnitOfWork } from "../../../pkg/infrastructure/mongodb/unit-of-work.ts";
import type { VersionedCollection } from "../../../pkg/infrastructure/mongodb/versioned-collection.ts";
import type {
  OwnerRelationshipWriter,
  RestaurantAccessControl,
  StaffRelationshipWriter,
} from "../applications/access-control.ts";
import { AssignStaffHandler } from "../applications/assign-staff/assign-staff.ts";
import { GetRestaurantHandler } from "../applications/get-restaurant/get-restaurant.ts";
import { RegisterRestaurantHandler } from "../applications/register-restaurant/register-restaurant.ts";
import { RevokeStaffHandler } from "../applications/revoke-staff/revoke-staff.ts";
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
  readonly restaurantAccessControl: RestaurantAccessControl;
  readonly ownerRelationships: OwnerRelationshipWriter;
  readonly staffRelationships: StaffRelationshipWriter;
  readonly spicedb: RelationshipsClient;
  readonly spicedbConfig: SpiceDbEnabledConfig;
  readonly reconcilerConfig: ReconcilerConfig;
}

/** What the slice provides to the process and to other slices. */
export interface RestaurantCradle {
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
  readonly registerRestaurant: RegisterRestaurantHandler;
  readonly assignStaff: AssignStaffHandler;
  readonly revokeStaff: RevokeStaffHandler;
  readonly relationshipReconciler: RelationshipReconciler;
}

type Cradle = RestaurantDependencies & RestaurantCradle;

export const registerRestaurant = (
  container: AwilixContainer<Cradle>,
): void => {
  const registrations: Registrations<RestaurantCradle> = {
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
    // Repairs owner/staff relationships of the dual-write (FR-445).
    relationshipReconciler: asFunction(
      ({
        spicedb,
        restaurantMemberships,
        reconcilerConfig,
        spicedbConfig,
        clock,
      }: Cradle) =>
        new RelationshipReconciler({
          client: spicedb,
          restaurants: restaurantMemberships,
          graceMs: reconcilerConfig.graceMs,
          pageSize: reconcilerConfig.pageSize,
          timeoutMs: spicedbConfig.timeoutMs,
          clock,
          log: (message) => console.log(`restaurants ${message}`),
        }),
    ).singleton(),
  };
  container.register(registrations);
};
