// Awilix registrations of the `cook` slice (ADR 5): the MongoDB repository of
// the Cook Pool, its use cases and the cook assignment of Start Preparation.
// The busy cooks and the queue come from the Restaurant Orders of the
// `restaurantorder` slice; every cook write notifies the kitchen board of its
// restaurant in the same Unit of Work (ADR 36).

import { type AwilixContainer, asFunction } from "awilix";
import type { Db } from "mongodb";
import type { Registrations } from "../../../di/container.ts";
import type { DatabaseUnitOfWork } from "../../../infrastructure/repository/unit-of-work.ts";
import type { Clock } from "../../../pkg/usecase/clock.ts";
import type { IdGenerator } from "../../../pkg/usecase/id-generator.ts";
import type { CookAssigner } from "../../restaurantorder/applications/start-preparation/start-preparation.ts";
import type { CookPoolAccess } from "../applications/access-control/access-control.ts";
import { PoolCookAssigner } from "../applications/assign-cook/assign-cook.ts";
import type { ChangeCookDependencies } from "../applications/change-cook/change-cook.ts";
import type {
  CookRestaurants,
  CookWorkload,
} from "../applications/cook-workload/cook-workload.ts";
import { GetCookPoolHandler } from "../applications/get-cook-pool/get-cook-pool.ts";
import { HireCookHandler } from "../applications/hire-cook/hire-cook.ts";
import { PutCookOnLineHandler } from "../applications/put-cook-on-line/put-cook-on-line.ts";
import { RetireCookHandler } from "../applications/retire-cook/retire-cook.ts";
import { TakeCookOffLineHandler } from "../applications/take-cook-off-line/take-cook-off-line.ts";
import type { CookRepository } from "../domains/cook/cook.ts";
import {
  type CookSaved,
  DatabaseCookRepository,
} from "../infrastructure/repository/cooks.ts";

/** Application-scope dependencies the slice takes from the process. */
export interface CookDependencies {
  readonly db: Db;
  readonly unitOfWork: DatabaseUnitOfWork;
  /** ADR 36: Change Notification of the kitchen board of every cook write. */
  readonly kitchenChanges: CookSaved;
  readonly clock: Clock;
  readonly ids: IdGenerator;
  readonly cookAccessControl: CookPoolAccess;
  /** Existence of restaurants, from the `restaurantorder` slice. */
  readonly restaurantExistence: CookRestaurants;
  /** Busy cooks and the queue, read from the Restaurant Orders. */
  readonly kitchenBoard: CookWorkload;
}

/** What the slice provides to the process. */
export interface CookCradle {
  readonly cooks: CookRepository;
  /** Start Preparation of the `restaurantorder` slice assigns cooks here. */
  readonly cookAssigner: CookAssigner;
  readonly hireCook: HireCookHandler;
  readonly putCookOnLine: PutCookOnLineHandler;
  readonly takeCookOffLine: TakeCookOffLineHandler;
  readonly retireCook: RetireCookHandler;
  readonly getCookPool: GetCookPoolHandler;
}

type Cradle = CookDependencies & CookCradle;

const changeDependencies = ({
  cookAccessControl,
  unitOfWork,
  cooks,
  kitchenBoard,
  clock,
}: Cradle): ChangeCookDependencies => ({
  accessControl: cookAccessControl,
  unitOfWork,
  cooks,
  workload: kitchenBoard,
  clock,
});

export const registerCook = (container: AwilixContainer<Cradle>): void => {
  const registrations: Registrations<CookCradle> = {
    cooks: asFunction(
      ({ db, unitOfWork, kitchenChanges }: Cradle) =>
        new DatabaseCookRepository(db, unitOfWork, kitchenChanges),
    ).singleton(),
    cookAssigner: asFunction(
      ({ cooks, kitchenBoard }: Cradle) =>
        new PoolCookAssigner({ cooks, workload: kitchenBoard }),
    ).singleton(),
    hireCook: asFunction(
      ({ cookAccessControl, restaurantExistence, cooks, ids }: Cradle) =>
        new HireCookHandler({
          accessControl: cookAccessControl,
          restaurants: restaurantExistence,
          cooks,
          ids,
        }),
    ).singleton(),
    putCookOnLine: asFunction(
      (cradle: Cradle) => new PutCookOnLineHandler(changeDependencies(cradle)),
    ).singleton(),
    takeCookOffLine: asFunction(
      (cradle: Cradle) =>
        new TakeCookOffLineHandler(changeDependencies(cradle)),
    ).singleton(),
    retireCook: asFunction(
      (cradle: Cradle) => new RetireCookHandler(changeDependencies(cradle)),
    ).singleton(),
    getCookPool: asFunction(
      ({ cookAccessControl, cooks, kitchenBoard }: Cradle) =>
        new GetCookPoolHandler({
          accessControl: cookAccessControl,
          cooks,
          workload: kitchenBoard,
        }),
    ).singleton(),
  };
  container.register(registrations);
};
