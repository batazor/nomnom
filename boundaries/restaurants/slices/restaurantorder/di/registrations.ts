// Awilix registrations of the `restaurantorder` slice (ADR 5): MongoDB adapters,
// MVP adapters of the open Acceptance decisions, the Outbox mapper and the
// handlers exposed through transport. Kitchen commands save the Restaurant
// Order and its published facts to the Outbox in one MongoDB transaction.

import { type AwilixContainer, asFunction, asValue } from "awilix";
import type { Db } from "mongodb";
import type { Registrations } from "../../../di/container.ts";
import type { OutboxEventMapper } from "../../../infrastructure/repository/outbox.ts";
import type { DatabaseUnitOfWork } from "../../../infrastructure/repository/unit-of-work.ts";
import type { KitchenConfig } from "../../../pkg/config/config.ts";
import type { Outbox } from "../../../pkg/outbox/outbox.ts";
import {
  command,
  query,
  type UseCase,
} from "../../../pkg/telemetry/usecase.ts";
import type { Clock } from "../../../pkg/usecase/clock.ts";
import type { IdGenerator } from "../../../pkg/usecase/id-generator.ts";
import type { MenuRepository } from "../../menu/domains/menu/menu.ts";
import type { WaitTimeStatisticsStore } from "../../restaurant/applications/record-wait-time/record-wait-time.ts";
import type { RestaurantRepository } from "../../restaurant/domains/restaurant/restaurant.ts";
import type { WaitTimePolicy } from "../../restaurant/domains/restaurant/wait-time.ts";
import {
  type AcceptanceConditionsProvider,
  type AcceptanceFactsReader,
  AcceptOrderHandler,
  type ReadyTimeEstimator,
} from "../applications/acceptance/accept-order/accept-order.ts";
import { ReceiveOrderRequestHandler } from "../applications/acceptance/receive-order-request/receive-order-request.ts";
import { RejectOrderHandler } from "../applications/acceptance/reject-order/reject-order.ts";
import type {
  FulfillmentCoordinatorAccess,
  FulfillmentProcessAccess,
  KitchenStaffAccess,
  RestaurantOrderViewAccess,
} from "../applications/access-control/access-control.ts";
import { CancelRestaurantOrderHandler } from "../applications/cancel-restaurant-order/cancel-restaurant-order.ts";
import {
  GetKitchenQueueHandler,
  type KitchenQueueOrdering,
} from "../applications/kitchen-board/get-kitchen-queue/get-kitchen-queue.ts";
import { ListActiveRestaurantOrdersHandler } from "../applications/kitchen-board/list-active-restaurant-orders/list-active-restaurant-orders.ts";
import { ListIncomingOrdersHandler } from "../applications/kitchen-board/list-incoming-orders/list-incoming-orders.ts";
import { ListScheduledOrdersHandler } from "../applications/kitchen-board/list-scheduled-orders/list-scheduled-orders.ts";
import type { KitchenCommandDependencies } from "../applications/kitchen-command/kitchen-command.ts";
import { MarkAssembledHandler } from "../applications/preparation/mark-assembled/mark-assembled.ts";
import { MarkReadyForPickupHandler } from "../applications/preparation/mark-ready-for-pickup/mark-ready-for-pickup.ts";
import { QueueRestaurantOrderHandler } from "../applications/preparation/queue-restaurant-order/queue-restaurant-order.ts";
import { ReportPreparationDelayHandler } from "../applications/preparation/report-preparation-delay/report-preparation-delay.ts";
import { ReportPreparationFailureHandler } from "../applications/preparation/report-preparation-failure/report-preparation-failure.ts";
import {
  type CookAssigner,
  StartPreparationHandler,
} from "../applications/preparation/start-preparation/start-preparation.ts";
import { RecordPickupHandler } from "../applications/record-pickup/record-pickup.ts";
import type { RestaurantOrderRepository } from "../domains/restaurantorder/restaurantorder.ts";
import { OpeningHoursAcceptanceConditions } from "../infrastructure/acceptance/opening-hours.ts";
import { WaitTimeReadyTimeEstimator } from "../infrastructure/acceptance/wait-time.ts";
import type { KitchenChangeNotifier } from "../infrastructure/integration/kitchen-changes.ts";
import { restaurantOrderEventMapper } from "../infrastructure/integration/restaurant-order-events.ts";
import { PriorityKitchenQueueOrdering } from "../infrastructure/kitchen-queue/priority.ts";
import {
  cancellationRejection,
  ObservedRestaurantOrderRepository,
  observeReceiveOrderRequest,
  observeTransitionRejections,
  type ReceiveOrderRequestPort,
  type RestaurantOrderDomainMetrics,
} from "../infrastructure/observability/domain-metrics.ts";
import {
  createRestaurantOrderCollection,
  DatabaseKitchenBoard,
  DatabaseRestaurantOrderRepository,
} from "../infrastructure/repository/restaurant-orders.ts";
import {
  RepositoryAcceptanceFactsReader,
  RepositoryRestaurantExistence,
} from "../infrastructure/restaurant-facts/restaurant-facts.ts";

/** Access ports of the slice, implemented together by the process adapter. */
export type KitchenAccess = KitchenStaffAccess &
  FulfillmentCoordinatorAccess &
  FulfillmentProcessAccess &
  RestaurantOrderViewAccess;

/** Application-scope dependencies the slice takes from the process. */
export interface RestaurantOrderDependencies {
  readonly db: Db;
  readonly unitOfWork: DatabaseUnitOfWork;
  readonly outbox: Outbox;
  /** ADR 36: Change Notification of every Restaurant Order write. */
  readonly kitchenChanges: KitchenChangeNotifier;
  readonly clock: Clock;
  readonly ids: IdGenerator;
  readonly kitchenAccessControl: KitchenAccess;
  readonly restaurants: RestaurantRepository;
  readonly menus: MenuRepository;
  readonly kitchenConfig: KitchenConfig;
  /** Cook Pool of the `cook` slice: Start Preparation assigns a free cook. */
  readonly cookAssigner: CookAssigner;
  /** Wait Time Statistics of the `restaurant` slice: initial `expected_ready_at`. */
  readonly restaurantWaitTimes: Pick<WaitTimeStatisticsStore, "load">;
  readonly waitTimePolicy: WaitTimePolicy;
  /**
   * Business and FSM metrics; the process also decorates its Unit of Work
   * and Outbox.
   */
  readonly domainMetrics: RestaurantOrderDomainMetrics;
}

/** What the slice provides to the process. */
export interface RestaurantOrderCradle {
  /**
   * Only Kitchen Order facts with a contract in events.proto are written to
   * the Outbox (explicit allowlist); Debezium publishes them (ADR 37).
   * Composed by `aggregate_type` with the other slices in di/restaurants.ts.
   */
  readonly restaurantOrderOutboxEventMapper: OutboxEventMapper;
  readonly restaurantOrders: RestaurantOrderRepository;
  /** Facts of the `restaurant` and `menu` slices read by Restaurant Orders. */
  readonly restaurantExistence: RepositoryRestaurantExistence;
  readonly acceptanceFacts: AcceptanceFactsReader;
  readonly acceptanceConditions: AcceptanceConditionsProvider;
  readonly readyTimeEstimator: ReadyTimeEstimator;
  /** Order in which Start Next Preparation takes queued orders (FR-241). */
  readonly kitchenQueueOrdering: KitchenQueueOrdering;
  readonly receiveOrderRequest: ReceiveOrderRequestPort;
  readonly cancelRestaurantOrder: CancelRestaurantOrderHandler;
  /** Queue Restaurant Order of `orders` at the planned start (FR-224). */
  readonly queueRestaurantOrder: QueueRestaurantOrderHandler;
  readonly recordPickup: RecordPickupHandler;
  readonly acceptRestaurantOrder: AcceptOrderHandler;
  readonly rejectRestaurantOrder: RejectOrderHandler;
  readonly startPreparation: StartPreparationHandler;
  readonly markRestaurantOrderAssembled: MarkAssembledHandler;
  readonly markRestaurantOrderReady: MarkReadyForPickupHandler;
  readonly reportPreparationDelay: ReportPreparationDelayHandler;
  readonly reportPreparationFailure: ReportPreparationFailureHandler;
  /** Read models of the kitchen board over the restaurant order documents. */
  readonly kitchenBoard: DatabaseKitchenBoard;
  readonly getKitchenQueue: GetKitchenQueueHandler;
  readonly listIncomingOrders: ListIncomingOrdersHandler;
  readonly listScheduledOrders: ListScheduledOrdersHandler;
  readonly listActiveRestaurantOrders: ListActiveRestaurantOrdersHandler;
}

/** Use cases of the slice with their telemetry names, by cradle key. */
export const RESTAURANT_ORDER_USE_CASES = {
  receiveOrderRequest: command("receive_order_request"),
  cancelRestaurantOrder: command("cancel_restaurant_order"),
  queueRestaurantOrder: command("queue_restaurant_order"),
  recordPickup: command("record_pickup"),
  listScheduledOrders: query("list_scheduled_orders"),
  acceptRestaurantOrder: command("accept_order"),
  rejectRestaurantOrder: command("reject_order"),
  startPreparation: command("start_preparation"),
  markRestaurantOrderAssembled: command("mark_assembled"),
  markRestaurantOrderReady: command("mark_ready_for_pickup"),
  reportPreparationDelay: command("report_preparation_delay"),
  reportPreparationFailure: command("report_preparation_failure"),
} as const satisfies Partial<Record<keyof RestaurantOrderCradle, UseCase>>;

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
    // Applied FSM transitions are recorded by the saves that commit them.
    restaurantOrders: asFunction(
      ({ db, unitOfWork, kitchenChanges, domainMetrics }: Cradle) =>
        new ObservedRestaurantOrderRepository(
          new DatabaseRestaurantOrderRepository(
            createRestaurantOrderCollection(db, unitOfWork),
            kitchenChanges,
          ),
          domainMetrics,
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
      ({ restaurants }: Cradle) =>
        new OpeningHoursAcceptanceConditions(restaurants),
    ).singleton(),
    readyTimeEstimator: asFunction(
      ({ restaurantWaitTimes, waitTimePolicy }: Cradle) =>
        new WaitTimeReadyTimeEstimator({
          store: restaurantWaitTimes,
          policy: waitTimePolicy,
        }),
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
    // Kitchen commands report the triggers the FSM table rejects.
    cancelRestaurantOrder: asFunction((cradle: Cradle) =>
      observeTransitionRejections(
        new CancelRestaurantOrderHandler({
          ...commandDependencies(cradle),
          cancellationDuringPreparation:
            cradle.kitchenConfig.cancellationDuringPreparation,
        }),
        cradle.domainMetrics,
        cancellationRejection,
      ),
    ).singleton(),
    queueRestaurantOrder: asFunction((cradle: Cradle) =>
      observeTransitionRejections(
        new QueueRestaurantOrderHandler({
          accessControl: cradle.kitchenAccessControl,
          restaurantOrders: cradle.restaurantOrders,
          clock: cradle.clock,
        }),
        cradle.domainMetrics,
      ),
    ).singleton(),
    recordPickup: asFunction((cradle: Cradle) =>
      observeTransitionRejections(
        new RecordPickupHandler({
          accessControl: cradle.kitchenAccessControl,
          unitOfWork: cradle.unitOfWork,
          restaurantOrders: cradle.restaurantOrders,
        }),
        cradle.domainMetrics,
      ),
    ).singleton(),
    acceptRestaurantOrder: asFunction((cradle: Cradle) =>
      observeTransitionRejections(
        new AcceptOrderHandler({
          ...commandDependencies(cradle),
          facts: cradle.acceptanceFacts,
          conditions: cradle.acceptanceConditions,
          readyTimeEstimator: cradle.readyTimeEstimator,
        }),
        cradle.domainMetrics,
      ),
    ).singleton(),
    rejectRestaurantOrder: asFunction((cradle: Cradle) =>
      observeTransitionRejections(
        new RejectOrderHandler(commandDependencies(cradle)),
        cradle.domainMetrics,
      ),
    ).singleton(),
    kitchenQueueOrdering: asFunction(
      ({ restaurantWaitTimes, waitTimePolicy, kitchenConfig }: Cradle) =>
        new PriorityKitchenQueueOrdering({
          store: restaurantWaitTimes,
          policy: waitTimePolicy,
          agingFactor: kitchenConfig.queueAgingFactor,
        }),
    ).singleton(),
    startPreparation: asFunction((cradle: Cradle) =>
      observeTransitionRejections(
        new StartPreparationHandler({
          ...commandDependencies(cradle),
          started: cradle.kitchenBoard,
          queue: cradle.kitchenBoard,
          ordering: cradle.kitchenQueueOrdering,
          cooks: cradle.cookAssigner,
          onRepeat: cradle.kitchenConfig.repeatPolicy,
        }),
        cradle.domainMetrics,
      ),
    ).singleton(),
    // Assembly is a local fact: a single document write without the Outbox.
    markRestaurantOrderAssembled: asFunction(
      ({
        kitchenAccessControl,
        restaurantOrders,
        clock,
        domainMetrics,
      }: Cradle) =>
        observeTransitionRejections(
          new MarkAssembledHandler({
            accessControl: kitchenAccessControl,
            restaurantOrders,
            clock,
          }),
          domainMetrics,
        ),
    ).singleton(),
    markRestaurantOrderReady: asFunction((cradle: Cradle) =>
      observeTransitionRejections(
        new MarkReadyForPickupHandler({
          ...commandDependencies(cradle),
          onRepeat: cradle.kitchenConfig.repeatPolicy,
        }),
        cradle.domainMetrics,
      ),
    ).singleton(),
    reportPreparationDelay: asFunction((cradle: Cradle) =>
      observeTransitionRejections(
        new ReportPreparationDelayHandler(commandDependencies(cradle)),
        cradle.domainMetrics,
      ),
    ).singleton(),
    reportPreparationFailure: asFunction((cradle: Cradle) =>
      observeTransitionRejections(
        new ReportPreparationFailureHandler(commandDependencies(cradle)),
        cradle.domainMetrics,
      ),
    ).singleton(),
    kitchenBoard: asFunction(
      ({ db, unitOfWork }: Cradle) =>
        new DatabaseKitchenBoard(
          createRestaurantOrderCollection(db, unitOfWork),
        ),
    ).singleton(),
    // The same order Start Next Preparation takes (FR-241).
    getKitchenQueue: asFunction(
      ({
        kitchenBoard,
        kitchenAccessControl,
        kitchenQueueOrdering,
        clock,
      }: Cradle) =>
        new GetKitchenQueueHandler({
          accessControl: kitchenAccessControl,
          reader: kitchenBoard,
          ordering: kitchenQueueOrdering,
          clock,
        }),
    ).singleton(),
    listIncomingOrders: asFunction(
      ({ kitchenBoard, kitchenAccessControl }: Cradle) =>
        new ListIncomingOrdersHandler({
          accessControl: kitchenAccessControl,
          reader: kitchenBoard,
        }),
    ).singleton(),
    listScheduledOrders: asFunction(
      ({ kitchenBoard, kitchenAccessControl }: Cradle) =>
        new ListScheduledOrdersHandler({
          accessControl: kitchenAccessControl,
          reader: kitchenBoard,
        }),
    ).singleton(),
    listActiveRestaurantOrders: asFunction(
      ({ kitchenBoard, kitchenAccessControl }: Cradle) =>
        new ListActiveRestaurantOrdersHandler({
          accessControl: kitchenAccessControl,
          reader: kitchenBoard,
        }),
    ).singleton(),
  };
  container.register(registrations);
};
