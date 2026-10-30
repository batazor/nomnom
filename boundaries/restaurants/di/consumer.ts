// Composition root of `cmd/restaurants-consumer` (ADR 5): Awilix registrations
// of the Kafka consumer of Integration Events (ADR 11, ADR 28). Every
// registration lives in the application scope: handlers are stateless over
// shared clients, and the resource of one message — the MongoDB session of
// its Inbox transaction — is owned by `DatabaseUnitOfWork.run` (async context)
// for exactly that message. The MongoDB connection, the dead letter producer
// and the not yet connected Kafka consumer client are acquired and released
// by cmd/restaurants-consumer.
import { type AwilixContainer, asFunction, asValue } from "awilix";
import type { Valkey } from "iovalkey";
import type { Db, MongoClient } from "mongodb";
import {
  type ConsumerClient,
  KafkaConsumerWorker,
} from "../infrastructure/kafka/consumer.ts";
import type {
  InboundMessage,
  MessagePublisher,
} from "../infrastructure/messaging/envelope.ts";
import { OtelConsumerMetrics } from "../infrastructure/messaging/metrics.ts";
import {
  type ConsumerMetrics,
  type EventHandler,
  type Inbox,
  MessageProcessor,
  type ProcessOutcome,
} from "../infrastructure/messaging/processor.ts";
import {
  HttpSchemaRegistry,
  type SchemaSubjects,
} from "../infrastructure/messaging/schema-registry.ts";
import type { DatabaseConnection } from "../infrastructure/repository/client.ts";
import { DatabaseInbox } from "../infrastructure/repository/inbox.ts";
import { DatabaseOutbox } from "../infrastructure/repository/outbox.ts";
import type { DatabaseUnitOfWork } from "../infrastructure/repository/unit-of-work.ts";
import { CacheMetrics } from "../infrastructure/valkey/read-cache.ts";
import { CacheRefreshHandler } from "../infrastructure/valkey/refresh.ts";
import type {
  ConsumerConfig,
  KitchenConfig,
  SchemaRegistryConfig,
  ValkeyConfig,
  WaitTimeConfig,
} from "../pkg/config/config.ts";
import { ParallelWorkers, type Worker } from "../pkg/lifecycle/lifecycle.ts";
import type { Logger } from "../pkg/telemetry/logger.ts";
import { currentTraceMetadata } from "../pkg/telemetry/trace-context.ts";
import { UseCaseTelemetry } from "../pkg/telemetry/usecase.ts";
import type { Clock } from "../pkg/usecase/clock.ts";
import {
  CachedMenuReader,
  menuCacheRefreshes,
} from "../slices/menu/infrastructure/cache/menus.ts";
import { MENU_EVENTS_TOPIC } from "../slices/menu/infrastructure/integration/menu-events.ts";
import {
  createMenuCollection,
  DatabaseMenuReader,
} from "../slices/menu/infrastructure/repository/menus.ts";
import { RecordWaitTimeHandler } from "../slices/restaurant/applications/record-wait-time/record-wait-time.ts";
import { WaitTimePolicy } from "../slices/restaurant/domains/restaurant/wait-time.ts";
import {
  CachedRestaurantCatalog,
  restaurantCacheRefreshes,
} from "../slices/restaurant/infrastructure/cache/restaurants.ts";
import {
  RESTAURANT_EVENTS_TOPIC,
  restaurantWaitTimeEventMapper,
} from "../slices/restaurant/infrastructure/integration/restaurant-events.ts";
import {
  createWaitTimeHandlers,
  WAIT_TIME_CONSUMER,
} from "../slices/restaurant/infrastructure/integration/wait-time-facts.ts";
import {
  createRestaurantCollection,
  DatabaseRestaurantCatalog,
  DatabaseRestaurantWaitTimes,
} from "../slices/restaurant/infrastructure/repository/restaurants.ts";
import type { PickupWitnessAccess } from "../slices/restaurantorder/applications/access-control.ts";
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
import { RESTAURANT_ORDER_EVENTS_TOPIC } from "../slices/restaurantorder/infrastructure/integration/restaurant-order-events.ts";
import {
  ObservedDatabaseUnitOfWork,
  ObservedRestaurantOrderRepository,
  RestaurantOrderDomainMetrics,
} from "../slices/restaurantorder/infrastructure/observability/domain-metrics.ts";
import {
  createRestaurantOrderCollection,
  DatabaseRestaurantOrderRepository,
} from "../slices/restaurantorder/infrastructure/repository/restaurant-orders.ts";
import { createProcessContainer, type Registrations } from "./container.ts";
import { CONSUMER_USE_CASES, instrumentUseCases } from "./usecases.ts";

/** Configuration and resources the composition root hands to the container. */
export type ConsumerResources = Readonly<{
  consumer: ConsumerConfig;
  /** Preparation time: the wait baseline until the sample is complete. */
  kitchen: KitchenConfig;
  waitTime: WaitTimeConfig;
  database: DatabaseConnection;
  /** Not yet connected; the worker connects and disconnects it. */
  kafkaConsumer: ConsumerClient;
  /** Consumer of the cache refresh group, not yet connected either. */
  cacheRefreshConsumer: ConsumerClient;
  valkeyConfig: ValkeyConfig;
  /** Opened by cmd/restaurants-consumer. */
  valkey: Valkey;
  /** Producer of `<stream>.dlq` dead letters. */
  deadLetters: MessagePublisher;
  /** Schema Registry of the records of the streams (FR-715). */
  schemaRegistry: SchemaRegistryConfig;
  logger: Logger;
}>;

export interface ConsumerCradle {
  readonly consumerConfig: ConsumerConfig;
  readonly databaseClient: MongoClient;
  readonly db: Db;
  readonly kafkaConsumer: ConsumerClient;
  readonly cacheRefreshConsumer: ConsumerClient;
  readonly valkeyConfig: ValkeyConfig;
  readonly valkey: Valkey;
  readonly cacheMetrics: CacheMetrics;
  /** ADR 43: opening and availability events rewrite the read cache keys. */
  readonly cacheRefresh: CacheRefreshHandler;
  readonly deadLetters: MessagePublisher;
  /** Subjects of the schema ids of the records (FR-715). */
  readonly schemas: SchemaSubjects;
  /** Structured stdout logs (pkg/telemetry/logger.ts). */
  readonly logger: Logger;
  /** Span and duration of every use case listed in di/usecases.ts. */
  readonly useCaseTelemetry: UseCaseTelemetry;
  readonly clock: Clock;
  readonly domainMetrics: RestaurantOrderDomainMetrics;
  readonly unitOfWork: DatabaseUnitOfWork;
  readonly restaurantOrders: RestaurantOrderRepository;
  readonly inbox: Inbox;
  readonly kitchenChanges: KitchenChangeNotifier;
  readonly consumerMetrics: ConsumerMetrics;
  /** Allows only Record Pickup from the delivery event stream. */
  readonly pickupWitnessAccess: PickupWitnessAccess;
  readonly recordPickup: RecordPickupHandler;
  readonly deliveryPickedUp: EventHandler;
  readonly deliveryEventsProcessor: MessageProcessor;
  readonly waitTimePolicy: WaitTimePolicy;
  /** Records the context's own Restaurant Order facts (FR-644). */
  readonly recordWaitTime: RecordWaitTimeHandler;
  readonly waitTimeProcessor: MessageProcessor;
  /**
   * Consumer group of `delivery.v1.delivery-events` and
   * `restaurants.v1.restaurant-order-events` and the cache refresh group of
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
    schemas: asFunction(
      () => new HttpSchemaRegistry(resources.schemaRegistry),
    ).singleton(),
    logger: asValue(resources.logger),
    useCaseTelemetry: asFunction(() => new UseCaseTelemetry()).singleton(),
    clock: asValue<Clock>({ now: () => new Date() }),
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
    inbox: asFunction(
      ({ db, unitOfWork, clock }: ConsumerCradle) =>
        new DatabaseInbox(db, unitOfWork, () => clock.now()),
    ).singleton(),
    consumerMetrics: asFunction(() => new OtelConsumerMetrics()).singleton(),
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
        schemas,
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
          schemas,
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
    waitTimePolicy: asFunction(
      () =>
        new WaitTimePolicy({
          ...resources.waitTime,
          preparationTimeMs: resources.kitchen.preparationTimeMs,
        }),
    ).singleton(),
    // RestaurantWaitLevelChanged goes to the Outbox of the statistics change.
    recordWaitTime: asFunction(
      ({ db, unitOfWork, waitTimePolicy, clock }: ConsumerCradle) =>
        new RecordWaitTimeHandler({
          unitOfWork,
          store: new DatabaseRestaurantWaitTimes(
            createRestaurantCollection(db, unitOfWork),
          ),
          policy: waitTimePolicy,
          outbox: new DatabaseOutbox(db, unitOfWork, {
            mapper: restaurantWaitTimeEventMapper,
            metadata: currentTraceMetadata,
          }),
          clock,
        }),
    ).singleton(),
    waitTimeProcessor: asFunction(
      ({
        consumerConfig,
        recordWaitTime,
        inbox,
        deadLetters,
        schemas,
        consumerMetrics,
        logger,
      }: ConsumerCradle) => {
        const log = logger.child({
          operation: "kafka.consume",
          "messaging.consumer.group.name": consumerConfig.groupId,
        });
        return new MessageProcessor({
          consumer: WAIT_TIME_CONSUMER,
          group: consumerConfig.groupId,
          handlers: createWaitTimeHandlers({ recordWaitTime }),
          inbox,
          deadLetters,
          schemas,
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
        waitTimePolicy,
        clock,
        schemas,
        logger,
      }: ConsumerCradle) => {
        const cache = {
          client: valkey,
          ttlMs: valkeyConfig.ttlMs,
          metrics: cacheMetrics,
        };
        const waitTime = {
          policy: waitTimePolicy,
          now: () => clock.now(),
        };
        return new CacheRefreshHandler({
          refreshes: {
            ...restaurantCacheRefreshes(
              new CachedRestaurantCatalog(
                new DatabaseRestaurantCatalog(
                  createRestaurantCollection(db, unitOfWork),
                  waitTime,
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
          schemas,
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
        deliveryEventsProcessor,
        waitTimeProcessor,
        consumerConfig,
        cacheRefreshConsumer,
        cacheRefresh,
        valkeyConfig,
      }: ConsumerCradle) =>
        new ParallelWorkers([
          new KafkaConsumerWorker({
            client: kafkaConsumer,
            topics: [DELIVERY_EVENTS_TOPIC, RESTAURANT_ORDER_EVENTS_TOPIC],
            handler: new TopicRouter({
              [DELIVERY_EVENTS_TOPIC]: deliveryEventsProcessor,
              [RESTAURANT_ORDER_EVENTS_TOPIC]: waitTimeProcessor,
            }),
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

/** One consumer group over several streams: each has its own processor. */
class TopicRouter {
  readonly #processors: Readonly<Record<string, MessageProcessor>>;

  constructor(processors: Readonly<Record<string, MessageProcessor>>) {
    this.#processors = processors;
  }

  process(
    message: InboundMessage,
    signal: AbortSignal,
  ): Promise<ProcessOutcome> {
    const processor = this.#processors[message.topic];
    if (processor === undefined) {
      throw new Error(`no processor for topic ${message.topic}`);
    }
    return processor.process(message, signal);
  }
}
