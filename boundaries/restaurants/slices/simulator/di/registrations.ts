// Awilix registrations of the `simulator` slice (ADR 5): the stored Simulator
// state, the operator use cases exposed through transport and the kitchen
// Simulator pass run by the process loop (FR-604, FR-605).
import { type AwilixContainer, asFunction } from "awilix";
import type { Db } from "mongodb";
import type { Registrations } from "../../../di/container.ts";
import type { SimulatorConfig } from "../../../pkg/config/config.ts";
import type { Logger } from "../../../pkg/telemetry/logger.ts";
import type { Clock } from "../../../pkg/usecase/clock.ts";
import type { RestaurantRepository } from "../../restaurant/domains/restaurant/restaurant.ts";
import type { RestaurantOrderCradle } from "../../restaurantorder/di/registrations.ts";
import type { SimulatorAccess } from "../applications/access-control/access-control.ts";
import { GetSimulatorHandler } from "../applications/get-simulator/get-simulator.ts";
import { SetSimulatorEnabledHandler } from "../applications/set-simulator-enabled/set-simulator-enabled.ts";
import { KitchenSimulator } from "../applications/simulate-kitchen/simulate-kitchen.ts";
import type { SimulatorStateRepository } from "../domains/simulator/simulator.ts";
import { BoardPendingKitchenOrders } from "../infrastructure/pending-kitchen-orders.ts";
import { DatabaseSimulatorState } from "../infrastructure/repository/simulator-state.ts";
import { RepositoryRestaurantOwners } from "../infrastructure/restaurant-owners.ts";

/** Application-scope dependencies the slice takes from the process. */
export interface SimulatorDependencies {
  readonly db: Db;
  readonly clock: Clock;
  readonly logger: Logger;
  readonly simulatorConfig: SimulatorConfig;
  readonly simulatorAccessControl: SimulatorAccess;
  /** Random number in `[0, 1)`: outcomes and delays of the Simulator. */
  readonly random: () => number;
  readonly restaurants: RestaurantRepository;
}

/** What the slice provides to the process. */
export interface SimulatorCradle {
  readonly simulatorState: SimulatorStateRepository;
  readonly getSimulator: GetSimulatorHandler;
  readonly setSimulatorEnabled: SetSimulatorEnabledHandler;
  readonly kitchenSimulator: KitchenSimulator;
}

type Cradle = SimulatorDependencies &
  SimulatorCradle &
  Pick<
    RestaurantOrderCradle,
    | "kitchenBoard"
    | "acceptRestaurantOrder"
    | "rejectRestaurantOrder"
    | "startPreparation"
    | "markRestaurantOrderAssembled"
    | "markRestaurantOrderReady"
    | "reportPreparationDelay"
    | "reportPreparationFailure"
  >;

export const registerSimulator = (container: AwilixContainer<Cradle>): void => {
  const registrations: Registrations<SimulatorCradle> = {
    simulatorState: asFunction(
      ({ db }: Cradle) => new DatabaseSimulatorState(db),
    ).singleton(),
    getSimulator: asFunction(
      ({ simulatorAccessControl, simulatorState }: Cradle) =>
        new GetSimulatorHandler({
          accessControl: simulatorAccessControl,
          state: simulatorState,
        }),
    ).singleton(),
    setSimulatorEnabled: asFunction(
      ({ simulatorAccessControl, simulatorState, clock }: Cradle) =>
        new SetSimulatorEnabledHandler({
          accessControl: simulatorAccessControl,
          state: simulatorState,
          clock,
        }),
    ).singleton(),
    // Kitchen commands are resolved from the cradle, so they are the
    // instrumented use cases of di/usecases.ts.
    kitchenSimulator: asFunction(
      (cradle: Cradle) =>
        new KitchenSimulator({
          state: cradle.simulatorState,
          orders: new BoardPendingKitchenOrders(cradle.kitchenBoard),
          owners: new RepositoryRestaurantOwners(cradle.restaurants),
          commands: {
            accept: cradle.acceptRestaurantOrder,
            reject: cradle.rejectRestaurantOrder,
            startPreparation: cradle.startPreparation,
            markAssembled: cradle.markRestaurantOrderAssembled,
            markReady: cradle.markRestaurantOrderReady,
            reportDelay: cradle.reportPreparationDelay,
            reportFailure: cradle.reportPreparationFailure,
          },
          clock: cradle.clock,
          random: cradle.random,
          delay: {
            minMs: cradle.simulatorConfig.minDelayMs,
            maxMs: cradle.simulatorConfig.maxDelayMs,
          },
          log: cradle.logger.child({ operation: "simulator" }).warn,
        }),
    ).singleton(),
  };
  container.register(registrations);
};
