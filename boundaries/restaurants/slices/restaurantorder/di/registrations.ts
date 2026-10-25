// Awilix registrations of the `restaurantorder` slice (ADR 5): MongoDB adapters,
// MVP adapters of the open Acceptance decisions, the Outbox mapper and the
// handlers exposed through transport. Kitchen commands save the Restaurant
// Order and its published facts to the Outbox in one MongoDB transaction.
import { type AwilixContainer, asFunction, asValue } from "awilix";
import type { Db } from "mongodb";

import type { Clock } from "../../../pkg/application/clock.ts";
import type { IdGenerator } from "../../../pkg/application/id-generator.ts";
import type { Outbox } from "../../../pkg/application/outbox.ts";
import type { KitchenConfig } from "../../../pkg/config/config.ts";
import type { Registrations } from "../../../pkg/di/container.ts";
import type { OutboxEventMapper } from "../../../pkg/infrastructure/mongodb/outbox.ts";
import type { MongoUnitOfWork } from "../../../pkg/infrastructure/mongodb/unit-of-work.ts";
import type { MenuRepository } from "../../menu/domains/menu/menu.ts";
import type { RestaurantRepository } from "../../restaurant/domains/restaurant/restaurant.ts";
import {
  type AcceptanceConditionsProvider,
  type AcceptanceFactsReader,
  AcceptOrderHandler,
  type ReadyTimeEstimator,
} from "../applications/accept-order/accept-order.ts";
import type {
  FulfillmentCoordinatorAccess,
  KitchenStaffAccess,
  RestaurantOrderViewAccess,
} from "../applications/access-control.ts";
import { CancelRestaurantOrderHandler } from "../applications/cancel-restaurant-order/cancel-restaurant-order.ts";
import type { KitchenCommandDependencies } from "../applications/kitchen-command.ts";
import { MarkAssembledHandler } from "../applications/mark-assembled/mark-assembled.ts";
import { MarkReadyForPickupHandler } from "../applications/mark-ready-for-pickup/mark-ready-for-pickup.ts";
import {
  ReceiveOrderRequestHandler,
  type RestaurantExistence,
} from "../applications/receive-order-request/receive-order-request.ts";
import { RejectOrderHandler } from "../applications/reject-order/reject-order.ts";
import { ReportPreparationDelayHandler } from "../applications/report-preparation-delay/report-preparation-delay.ts";
import { ReportPreparationFailureHandler } from "../applications/report-preparation-failure/report-preparation-failure.ts";
import { StartPreparationHandler } from "../applications/start-preparation/start-preparation.ts";
import type { RestaurantOrderRepository } from "../domains/restaurantorder/restaurantorder.ts";
import {
  FixedPreparationTimeEstimator,
  UnconstrainedAcceptanceConditions,
} from "../infrastructure/acceptance/mvp.ts";
import { restaurantOrderEventMapper } from "../infrastructure/integration/restaurant-order-events.ts";
import {
  createRestaurantOrderCollection,
  MongoRestaurantOrderRepository,
} from "../infrastructure/mongodb/restaurant-orders.ts";
import {
  observeReceiveOrderRequest,
  type ReceiveOrderRequestPort,
  type RestaurantOrderDomainMetrics,
} from "../infrastructure/observability/domain-metrics.ts";
import {
  RepositoryAcceptanceFactsReader,
  RepositoryRestaurantExistence,
} from "../infrastructure/restaurant-facts/restaurant-facts.ts";

/** Access ports of the slice, implemented together by the process adapter. */
export type KitchenAccess = KitchenStaffAccess &
  FulfillmentCoordinatorAccess &
  RestaurantOrderViewAccess;

/** Application-scope dependencies the slice takes from the process. */
export interface RestaurantOrderDependencies {
  readonly db: Db;
  readonly unitOfWork: MongoUnitOfWork;
  readonly outbox: Outbox;
  readonly clock: Clock;
  readonly ids: IdGenerator;
  readonly kitchenAccessControl: KitchenAccess;
  readonly restaurants: RestaurantRepository;
  readonly menus: MenuRepository;
  readonly kitchenConfig: KitchenConfig;
  /** Business metrics; the process also decorates its Unit of Work and Outbox. */
  readonly domainMetrics: RestaurantOrderDomainMetrics;
}

/** What the slice provides to the process. */
export interface RestaurantOrderCradle {
  /**
   * Only Kitchen Order facts with a contract in events.proto are written to
   * the Outbox (explicit allowlist); restaurants-relay publishes them (ADR 31).
   * Composed by `aggregate_type` with the other slices in di/restaurants.ts.
   */
  readonly restaurantOrderOutboxEventMapper: OutboxEventMapper;
  readonly restaurantOrders: RestaurantOrderRepository;
  /** Facts of the `restaurant` and `menu` slices read by Restaurant Orders. */
  readonly restaurantExistence: RestaurantExistence;
  readonly acceptanceFacts: AcceptanceFactsReader;
  readonly acceptanceConditions: AcceptanceConditionsProvider;
  readonly readyTimeEstimator: ReadyTimeEstimator;
  readonly receiveOrderRequest: ReceiveOrderRequestPort;
  readonly cancelRestaurantOrder: CancelRestaurantOrderHandler;
  readonly acceptRestaurantOrder: AcceptOrderHandler;
  readonly rejectRestaurantOrder: RejectOrderHandler;
  readonly startPreparation: StartPreparationHandler;
  readonly markRestaurantOrderAssembled: MarkAssembledHandler;
  readonly markRestaurantOrderReady: MarkReadyForPickupHandler;
  readonly reportPreparationDelay: ReportPreparationDelayHandler;
  readonly reportPreparationFailure: ReportPreparationFailureHandler;
}

type Cradle = RestaurantOrderDependencies & RestaurantOrderCradle;

/** Dependencies shared by the kitchen commands that write to the Outbox. */
const commandDependencies = ({
  kitchenAccessControl,
  unitOfWork,
  restaurantOrders,
  outbox,
  clock,
}: Cradle): KitchenCommandDependencies & { accessControl: KitchenAccess } => ({
  accessControl: kitchenAccessControl,
  unitOfWork,
  restaurantOrders,
  outbox,
  clock,
});

export const registerRestaurantOrder = (
  container: AwilixContainer<Cradle>,
): void => {
  const registrations: Registrations<RestaurantOrderCradle> = {
    restaurantOrderOutboxEventMapper: asValue(restaurantOrderEventMapper),
    restaurantOrders: asFunction(
      ({ db, unitOfWork }: Cradle) =>
        new MongoRestaurantOrderRepository(
          createRestaurantOrderCollection(db, unitOfWork),
        ),
    ).singleton(),
    restaurantExistence: asFunction(
      ({ restaurants }: Cradle) =>
        new RepositoryRestaurantExistence(restaurants),
    ).singleton(),
    acceptanceFacts: asFunction(
      ({ restaurants, menus }: Cradle) =>
        new RepositoryAcceptanceFactsReader({ restaurants, menus }),
    ).singleton(),
    acceptanceConditions: asFunction(
      () => new UnconstrainedAcceptanceConditions(),
    ).singleton(),
    readyTimeEstimator: asFunction(
      ({ kitchenConfig }: Cradle) =>
        new FixedPreparationTimeEstimator(kitchenConfig.preparationTimeMs),
    ).singleton(),
    receiveOrderRequest: asFunction(
      ({
        kitchenAccessControl,
        restaurantExistence,
        restaurantOrders,
        ids,
        clock,
        domainMetrics,
      }: Cradle) =>
        observeReceiveOrderRequest(
          new ReceiveOrderRequestHandler({
            accessControl: kitchenAccessControl,
            restaurants: restaurantExistence,
            restaurantOrders,
            ids,
            clock,
          }),
          domainMetrics,
        ),
    ).singleton(),
    cancelRestaurantOrder: asFunction(
      (cradle: Cradle) =>
        new CancelRestaurantOrderHandler({
          ...commandDependencies(cradle),
          cancellationDuringPreparation:
            cradle.kitchenConfig.cancellationDuringPreparation,
        }),
    ).singleton(),
    acceptRestaurantOrder: asFunction(
      (cradle: Cradle) =>
        new AcceptOrderHandler({
          ...commandDependencies(cradle),
          facts: cradle.acceptanceFacts,
          conditions: cradle.acceptanceConditions,
          readyTimeEstimator: cradle.readyTimeEstimator,
        }),
    ).singleton(),
    rejectRestaurantOrder: asFunction(
      (cradle: Cradle) => new RejectOrderHandler(commandDependencies(cradle)),
    ).singleton(),
    startPreparation: asFunction(
      (cradle: Cradle) =>
        new StartPreparationHandler({
          ...commandDependencies(cradle),
          onRepeat: cradle.kitchenConfig.repeatPolicy,
        }),
    ).singleton(),
    // Assembly is a local fact: a single document write without the Outbox.
    markRestaurantOrderAssembled: asFunction(
      ({ kitchenAccessControl, restaurantOrders, clock }: Cradle) =>
        new MarkAssembledHandler({
          accessControl: kitchenAccessControl,
          restaurantOrders,
          clock,
        }),
    ).singleton(),
    markRestaurantOrderReady: asFunction(
      (cradle: Cradle) =>
        new MarkReadyForPickupHandler({
          ...commandDependencies(cradle),
          onRepeat: cradle.kitchenConfig.repeatPolicy,
        }),
    ).singleton(),
    reportPreparationDelay: asFunction(
      (cradle: Cradle) =>
        new ReportPreparationDelayHandler(commandDependencies(cradle)),
    ).singleton(),
    reportPreparationFailure: asFunction(
      (cradle: Cradle) =>
        new ReportPreparationFailureHandler(commandDependencies(cradle)),
    ).singleton(),
  };
  container.register(registrations);
};
