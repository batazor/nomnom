// Composition root of `cmd/restaurants-consumer` (ADR 5): Awilix registrations
// of the Kafka consumer of Integration Events (ADR 11, ADR 28). Every
// registration lives in the application scope: handlers are stateless over
// shared clients, and the resource of one message — the MongoDB session of
// its Inbox transaction — is owned by `DatabaseUnitOfWork.run` (async context)
// for exactly that message. The MongoDB connection, the dead letter producer
// and the not yet connected Kafka consumer client are acquired and released
// by cmd/restaurants-consumer.
import { randomUUID } from "node:crypto";

import { type AwilixContainer, asFunction, asValue } from "awilix";
import type { Valkey } from "iovalkey";
import type { Db, MongoClient } from "mongodb";

import type { Clock } from "../pkg/application/clock.ts";
import type { IdGenerator } from "../pkg/application/id-generator.ts";
import type {
  ConsumerConfig,
  ValkeyEnabledConfig,
} from "../pkg/config/config.ts";
import {
  createProcessContainer,
  type Registrations,
} from "../pkg/di/container.ts";
import {
  type ConsumerClient,
  KafkaConsumerWorker,
} from "../pkg/infrastructure/kafka/consumer.ts";
import type { MessagePublisher } from "../pkg/infrastructure/messaging/envelope.ts";
import { OtelConsumerMetrics } from "../pkg/infrastructure/messaging/metrics.ts";
import {
  type ConsumerMetrics,
  type EventHandler,
  type Inbox,
  MessageProcessor,
} from "../pkg/infrastructure/messaging/processor.ts";
import type { DatabaseConnection } from "../pkg/infrastructure/repository/client.ts";
import { DatabaseInbox } from "../pkg/infrastructure/repository/inbox.ts";
import { DatabaseOutbox } from "../pkg/infrastructure/repository/outbox.ts";
import type { DatabaseUnitOfWork } from "../pkg/infrastructure/repository/unit-of-work.ts";
import { CacheMetrics } from "../pkg/infrastructure/valkey/read-cache.ts";
import { CacheRefreshHandler } from "../pkg/infrastructure/valkey/refresh.ts";
import { ParallelWorkers, type Worker } from "../pkg/lifecycle/lifecycle.ts";
import type { Logger } from "../pkg/telemetry/logger.ts";
import { currentTraceMetadata } from "../pkg/telemetry/trace-context.ts";
import { UseCaseTelemetry } from "../pkg/telemetry/usecase.ts";
import {
  CachedMenuReader,
  menuCacheRefreshes,
} from "../slices/menu/infrastructure/cache/menus.ts";
import { MENU_EVENTS_TOPIC } from "../slices/menu/infrastructure/integration/menu-events.ts";
import {
  createMenuCollection,
  DatabaseMenuReader,
} from "../slices/menu/infrastructure/repository/menus.ts";
import type { RestaurantRepository } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import {
  CachedRestaurantCatalog,
  restaurantCacheRefreshes,
} from "../slices/restaurant/infrastructure/cache/restaurants.ts";
import { RESTAURANT_EVENTS_TOPIC } from "../slices/restaurant/infrastructure/integration/restaurant-events.ts";
import {
  createRestaurantCollection,
  DatabaseRestaurantCatalog,
  DatabaseRestaurantRepository,
} from "../slices/restaurant/infrastructure/repository/restaurants.ts";
import type {
  FulfillmentCoordinatorAccess,
  PickupWitnessAccess,
} from "../slices/restaurantorder/applications/access-control.ts";
import { ReceiveOrderRequestHandler } from "../slices/restaurantorder/applications/receive-order-request/receive-order-request.ts";
import { RecordPickupHandler } from "../slices/restaurantorder/applications/record-pickup/record-pickup.ts";
import type { RestaurantOrderRepository } from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import {
  createDeliveryPickedUpHandler,
  DELIVERY_EVENTS_TOPIC,
  DELIVERY_PICKED_UP_CONSUMER,
  DeliveryEventSourceAccessControl,
} from "../slices/restaurantorder/infrastructure/integration/delivery-picked-up.ts";
import {
  type KitchenChangeNotifier,
  kitchenChangeNotifier,
} from "../slices/restaurantorder/infrastructure/integration/kitchen-changes.ts";
import {
  createOrderPlacedHandler,
  ORDER_EVENTS_TOPIC,
  ORDER_PLACED_CONSUMER,
  OrdersEventSourceAccessControl,
} from "../slices/restaurantorder/infrastructure/integration/order-placed.ts";
import {
  ObservedDatabaseUnitOfWork,
  ObservedRestaurantOrderRepository,
  observeReceiveOrderRequest,
  type ReceiveOrderRequestPort,
  RestaurantOrderDomainMetrics,
} from "../slices/restaurantorder/infrastructure/observability/domain-metrics.ts";
import {
  createRestaurantOrderCollection,
  DatabaseRestaurantOrderRepository,
} from "../slices/restaurantorder/infrastructure/repository/restaurant-orders.ts";
import { RepositoryRestaurantExistence } from "../slices/restaurantorder/infrastructure/restaurant-facts/restaurant-facts.ts";
import { CONSUMER_USE_CASES, instrumentUseCases } from "./usecases.ts";

/** Configuration and resources the composition root hands to the container. */
export type ConsumerResources = Readonly<{
  consumer: ConsumerConfig;
  database: DatabaseConnection;
  /** Not yet connected; the worker connects and disconnects it. */
  kafkaConsumer: ConsumerClient;
  /** Consumer of the cache refresh group, not yet connected either. */
  cacheRefreshConsumer: ConsumerClient;
  valkeyConfig: ValkeyEnabledConfig;
  /** Opened by cmd/restaurants-consumer. */
  valkey: Valkey;
  /** Producer of `<stream>.dlq` dead letters. */
  deadLetters: MessagePublisher;
  logger: Logger;
}>;

export interface ConsumerCradle {
  readonly consumerConfig: ConsumerConfig;
  readonly databaseClient: MongoClient;
  readonly db: Db;
  readonly kafkaConsumer: ConsumerClient;
  readonly cacheRefreshConsumer: ConsumerClient;
  readonly valkeyConfig: ValkeyEnabledConfig;
  readonly valkey: Valkey;
  readonly cacheMetrics: CacheMetrics;
  /** ADR 40: opening and availability events rewrite the read cache keys. */
  readonly cacheRefresh: CacheRefreshHandler;
  readonly deadLetters: MessagePublisher;
  /** Structured stdout logs (pkg/telemetry/logger.ts). */
  readonly logger: Logger;
  /** Span and duration of every use case listed in di/usecases.ts. */
  readonly useCaseTelemetry: UseCaseTelemetry;
  readonly clock: Clock;
  readonly ids: IdGenerator;
  readonly domainMetrics: RestaurantOrderDomainMetrics;
  readonly unitOfWork: DatabaseUnitOfWork;
  readonly restaurants: RestaurantRepository;
  readonly restaurantExistence: RepositoryRestaurantExistence;
  readonly restaurantOrders: RestaurantOrderRepository;
  /** Allows only Receive Order Request from the orders event stream. */
  readonly fulfillmentAccess: FulfillmentCoordinatorAccess;
  readonly receiveOrderRequest: ReceiveOrderRequestPort;
  readonly inbox: Inbox;
  readonly kitchenChanges: KitchenChangeNotifier;
  readonly consumerMetrics: ConsumerMetrics;
  readonly orderPlaced: EventHandler;
  readonly messageProcessor: MessageProcessor;
  /** Allows only Record Pickup from the delivery event stream. */
  readonly pickupWitnessAccess: PickupWitnessAccess;
  readonly recordPickup: RecordPickupHandler;
  readonly deliveryPickedUp: EventHandler;
  readonly deliveryEventsProcessor: MessageProcessor;
  /**
   * Consumer group of `orders.v1.order-events` and
   * `delivery.v1.delivery-events` and the cache refresh group of
   * `restaurants.v1.restaurant-events` and `restaurants.v1.menu-events`, run
   * by the Lifecycle.
   */
  readonly worker: Worker;
}

export const createConsumerContainer = (
  resources: ConsumerResources,
): AwilixContainer<ConsumerCradle> => {
  const container = createProcessContainer<ConsumerCradle>();
  const registrations: Registrations<ConsumerCradle> = {
    consumerConfig: asValue(resources.consumer),
    databaseClient: asValue(resources.database.client),
    db: asValue(resources.database.db),
    kafkaConsumer: asValue(resources.kafkaConsumer),
    cacheRefreshConsumer: asValue(resources.cacheRefreshConsumer),
    valkeyConfig: asValue(resources.valkeyConfig),
    valkey: asValue(resources.valkey),
    cacheMetrics: asFunction(() => new CacheMetrics()).singleton(),
    deadLetters: asValue(resources.deadLetters),
    logger: asValue(resources.logger),
    useCaseTelemetry: asFunction(() => new UseCaseTelemetry()).singleton(),
    clock: asValue<Clock>({ now: () => new Date() }),
    ids: asValue<IdGenerator>({ next: () => randomUUID() }),
    // `nomnom.restaurants.orders.received` is recorded after the Inbox
    // transaction of the message commits; an Inbox duplicate records nothing.
    domainMetrics: asFunction(
      () => new RestaurantOrderDomainMetrics(),
    ).singleton(),
    unitOfWork: asFunction(
      ({ databaseClient, domainMetrics }: ConsumerCradle) =>
        new ObservedDatabaseUnitOfWork(databaseClient, domainMetrics),
    ).singleton(),
    // ADR 36: every Restaurant Order the consumer writes changes the kitchen
    // board, so its Change Notification goes to the same Outbox transaction.
    kitchenChanges: asFunction(({ db, unitOfWork, clock }: ConsumerCradle) =>
      kitchenChangeNotifier(
        unitOfWork,
        new DatabaseOutbox(db, unitOfWork, { metadata: currentTraceMetadata }),
        () => clock.now(),
      ),
    ).singleton(),
    restaurants: asFunction(
      ({ db, unitOfWork, kitchenChanges }: ConsumerCradle) =>
        new DatabaseRestaurantRepository(
          createRestaurantCollection(db, unitOfWork),
          kitchenChanges,
        ),
    ).singleton(),
    restaurantExistence: asFunction(
      ({ restaurants }: ConsumerCradle) =>
        new RepositoryRestaurantExistence(restaurants),
    ).singleton(),
    restaurantOrders: asFunction(
      ({ db, unitOfWork, kitchenChanges, domainMetrics }: ConsumerCradle) =>
        new ObservedRestaurantOrderRepository(
          new DatabaseRestaurantOrderRepository(
            createRestaurantOrderCollection(db, unitOfWork),
            kitchenChanges,
          ),
          domainMetrics,
        ),
    ).singleton(),
    fulfillmentAccess: asFunction(
      () => new OrdersEventSourceAccessControl(),
    ).singleton(),
    receiveOrderRequest: asFunction(
      ({
        fulfillmentAccess,
        restaurantExistence,
        restaurantOrders,
        ids,
        clock,
        domainMetrics,
      }: ConsumerCradle) =>
        observeReceiveOrderRequest(
          new ReceiveOrderRequestHandler({
            accessControl: fulfillmentAccess,
            restaurants: restaurantExistence,
            restaurantOrders,
            ids,
            clock,
          }),
          domainMetrics,
        ),
    ).singleton(),
    inbox: asFunction(
      ({ db, unitOfWork, clock }: ConsumerCradle) =>
        new DatabaseInbox(db, unitOfWork, () => clock.now()),
    ).singleton(),
    consumerMetrics: asFunction(() => new OtelConsumerMetrics()).singleton(),
    orderPlaced: asFunction(({ receiveOrderRequest }: ConsumerCradle) =>
      createOrderPlacedHandler({ receiveOrderRequest }),
    ).singleton(),
    messageProcessor: asFunction(
      ({
        consumerConfig,
        orderPlaced,
        inbox,
        deadLetters,
        consumerMetrics,
        logger,
      }: ConsumerCradle) => {
        const log = logger.child({
          operation: "kafka.consume",
          "messaging.consumer.group.name": consumerConfig.groupId,
        });
        return new MessageProcessor({
          consumer: ORDER_PLACED_CONSUMER,
          group: consumerConfig.groupId,
          handlers: [orderPlaced],
          inbox,
          deadLetters,
          retry: {
            maxRetries: consumerConfig.maxRetries,
            initialIntervalMs: consumerConfig.retryInitialIntervalMs,
            maxIntervalMs: consumerConfig.retryMaxIntervalMs,
          },
          metrics: consumerMetrics,
          log: log.info,
          warn: log.warn,
        });
      },
    ).singleton(),
    pickupWitnessAccess: asFunction(
      () => new DeliveryEventSourceAccessControl(),
    ).singleton(),
    recordPickup: asFunction(
      ({ pickupWitnessAccess, unitOfWork, restaurantOrders }: ConsumerCradle) =>
        new RecordPickupHandler({
          accessControl: pickupWitnessAccess,
          unitOfWork,
          restaurantOrders,
        }),
    ).singleton(),
    deliveryPickedUp: asFunction(({ recordPickup }: ConsumerCradle) =>
      createDeliveryPickedUpHandler({ recordPickup }),
    ).singleton(),
    // Own Inbox name: the handler of the delivery stream is a consumer of its
    // own (ADR 11), in the same consumer group.
    deliveryEventsProcessor: asFunction(
      ({
        consumerConfig,
        deliveryPickedUp,
        inbox,
        deadLetters,
        consumerMetrics,
        logger,
      }: ConsumerCradle) => {
        const log = logger.child({
          operation: "kafka.consume",
          "messaging.consumer.group.name": consumerConfig.groupId,
        });
        return new MessageProcessor({
          consumer: DELIVERY_PICKED_UP_CONSUMER,
          group: consumerConfig.groupId,
          handlers: [deliveryPickedUp],
          inbox,
          deadLetters,
          retry: {
            maxRetries: consumerConfig.maxRetries,
            initialIntervalMs: consumerConfig.retryInitialIntervalMs,
            maxIntervalMs: consumerConfig.retryMaxIntervalMs,
          },
          metrics: consumerMetrics,
          log: log.info,
          warn: log.warn,
        });
      },
    ).singleton(),
    cacheRefresh: asFunction(
      ({
        db,
        unitOfWork,
        valkey,
        valkeyConfig,
        cacheMetrics,
        consumerConfig,
        logger,
      }: ConsumerCradle) => {
        const cache = {
          client: valkey,
          ttlMs: valkeyConfig.ttlMs,
          metrics: cacheMetrics,
        };
        return new CacheRefreshHandler({
          refreshes: {
            ...restaurantCacheRefreshes(
              new CachedRestaurantCatalog(
                new DatabaseRestaurantCatalog(
                  createRestaurantCollection(db, unitOfWork),
                ),
                cache,
              ),
            ),
            ...menuCacheRefreshes(
              new CachedMenuReader(
                new DatabaseMenuReader(createMenuCollection(db, unitOfWork)),
                cache,
              ),
            ),
          },
          retry: {
            initialIntervalMs: consumerConfig.retryInitialIntervalMs,
            maxIntervalMs: consumerConfig.retryMaxIntervalMs,
          },
          warn: logger.child({
            operation: "cache.refresh",
            "messaging.consumer.group.name": valkeyConfig.refreshGroup,
          }).warn,
        });
      },
    ).singleton(),
    worker: asFunction(
      ({
        kafkaConsumer,
        messageProcessor,
        deliveryEventsProcessor,
        consumerConfig,
        cacheRefreshConsumer,
        cacheRefresh,
        valkeyConfig,
      }: ConsumerCradle) =>
        new ParallelWorkers([
          new KafkaConsumerWorker({
            client: kafkaConsumer,
            topics: [ORDER_EVENTS_TOPIC, DELIVERY_EVENTS_TOPIC],
            // Each stream goes to the processor of its consumer.
            handler: {
              process: (message, signal) => {
                const processor = {
                  [ORDER_EVENTS_TOPIC]: messageProcessor,
                  [DELIVERY_EVENTS_TOPIC]: deliveryEventsProcessor,
                }[message.topic];
                if (processor === undefined) {
                  throw new Error(`no processor for topic ${message.topic}`);
                }
                return processor.process(message, signal);
              },
            },
            groupId: consumerConfig.groupId,
          }),
          new KafkaConsumerWorker({
            client: cacheRefreshConsumer,
            topics: [RESTAURANT_EVENTS_TOPIC, MENU_EVENTS_TOPIC],
            handler: cacheRefresh,
            groupId: valkeyConfig.refreshGroup,
          }),
        ]),
    ).singleton(),
  };
  container.register(registrations);
  instrumentUseCases(container, CONSUMER_USE_CASES);
  return container;
};
