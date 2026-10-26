// Composition root of `cmd/restaurants-consumer` (ADR 5): Awilix registrations
// of the Kafka consumer of Integration Events (ADR 11, ADR 28). Every
// registration lives in the application scope: handlers are stateless over
// shared clients, and the resource of one message — the MongoDB session of
// its Inbox transaction — is owned by `MongoUnitOfWork.run` (async context)
// for exactly that message. The MongoDB connection, the dead letter producer
// and the not yet connected Kafka consumer client are acquired and released
// by cmd/restaurants-consumer.
import { randomUUID } from "node:crypto";

import { type AwilixContainer, asFunction, asValue } from "awilix";
import type { Db, MongoClient } from "mongodb";

import type { Clock } from "../pkg/application/clock.ts";
import type { IdGenerator } from "../pkg/application/id-generator.ts";
import type { ConsumerConfig } from "../pkg/config/config.ts";
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
import type { MongoDbConnection } from "../pkg/infrastructure/mongodb/client.ts";
import { MongoInbox } from "../pkg/infrastructure/mongodb/inbox.ts";
import { MongoOutbox } from "../pkg/infrastructure/mongodb/outbox.ts";
import type { MongoUnitOfWork } from "../pkg/infrastructure/mongodb/unit-of-work.ts";
import type { Worker } from "../pkg/lifecycle/lifecycle.ts";
import type { Logger } from "../pkg/telemetry/logger.ts";
import { currentTraceMetadata } from "../pkg/telemetry/trace-context.ts";
import { UseCaseTelemetry } from "../pkg/telemetry/usecase.ts";
import type { RestaurantRepository } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import {
  createRestaurantCollection,
  MongoRestaurantRepository,
} from "../slices/restaurant/infrastructure/mongodb/restaurants.ts";
import type { FulfillmentCoordinatorAccess } from "../slices/restaurantorder/applications/access-control.ts";
import {
  ReceiveOrderRequestHandler,
  type RestaurantExistence,
} from "../slices/restaurantorder/applications/receive-order-request/receive-order-request.ts";
import type { RestaurantOrderRepository } from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
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
  createRestaurantOrderCollection,
  MongoRestaurantOrderRepository,
} from "../slices/restaurantorder/infrastructure/mongodb/restaurant-orders.ts";
import {
  ObservedMongoUnitOfWork,
  ObservedRestaurantOrderRepository,
  observeReceiveOrderRequest,
  type ReceiveOrderRequestPort,
  RestaurantOrderDomainMetrics,
} from "../slices/restaurantorder/infrastructure/observability/domain-metrics.ts";
import { RepositoryRestaurantExistence } from "../slices/restaurantorder/infrastructure/restaurant-facts/restaurant-facts.ts";
import { CONSUMER_USE_CASES, instrumentUseCases } from "./usecases.ts";

/** Configuration and resources the composition root hands to the container. */
export type ConsumerResources = Readonly<{
  consumer: ConsumerConfig;
  mongo: MongoDbConnection;
  /** Not yet connected; the worker connects and disconnects it. */
  kafkaConsumer: ConsumerClient;
  /** Producer of `<stream>.dlq` dead letters. */
  deadLetters: MessagePublisher;
  logger: Logger;
}>;

export interface ConsumerCradle {
  readonly consumerConfig: ConsumerConfig;
  readonly mongoClient: MongoClient;
  readonly db: Db;
  readonly kafkaConsumer: ConsumerClient;
  readonly deadLetters: MessagePublisher;
  /** Structured stdout logs (pkg/telemetry/logger.ts). */
  readonly logger: Logger;
  /** Span and duration of every use case listed in di/usecases.ts. */
  readonly useCaseTelemetry: UseCaseTelemetry;
  readonly clock: Clock;
  readonly ids: IdGenerator;
  readonly domainMetrics: RestaurantOrderDomainMetrics;
  readonly unitOfWork: MongoUnitOfWork;
  readonly restaurants: RestaurantRepository;
  readonly restaurantExistence: RestaurantExistence;
  readonly restaurantOrders: RestaurantOrderRepository;
  /** Allows only Receive Order Request from the orders event stream. */
  readonly fulfillmentAccess: FulfillmentCoordinatorAccess;
  readonly receiveOrderRequest: ReceiveOrderRequestPort;
  readonly inbox: Inbox;
  readonly kitchenChanges: KitchenChangeNotifier;
  readonly consumerMetrics: ConsumerMetrics;
  readonly orderPlaced: EventHandler;
  readonly messageProcessor: MessageProcessor;
  /** Consumer group of `orders.v1.order-events`, run by the Lifecycle. */
  readonly worker: Worker;
}

export const createConsumerContainer = (
  resources: ConsumerResources,
): AwilixContainer<ConsumerCradle> => {
  const container = createProcessContainer<ConsumerCradle>();
  const registrations: Registrations<ConsumerCradle> = {
    consumerConfig: asValue(resources.consumer),
    mongoClient: asValue(resources.mongo.client),
    db: asValue(resources.mongo.db),
    kafkaConsumer: asValue(resources.kafkaConsumer),
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
      ({ mongoClient, domainMetrics }: ConsumerCradle) =>
        new ObservedMongoUnitOfWork(mongoClient, domainMetrics),
    ).singleton(),
    // ADR 36: every Restaurant Order the consumer writes changes the kitchen
    // board, so its Change Notification goes to the same Outbox transaction.
    kitchenChanges: asFunction(({ db, unitOfWork, clock }: ConsumerCradle) =>
      kitchenChangeNotifier(
        unitOfWork,
        new MongoOutbox(db, unitOfWork, { metadata: currentTraceMetadata }),
        () => clock.now(),
      ),
    ).singleton(),
    restaurants: asFunction(
      ({ db, unitOfWork, kitchenChanges }: ConsumerCradle) =>
        new MongoRestaurantRepository(
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
          new MongoRestaurantOrderRepository(
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
        new MongoInbox(db, unitOfWork, () => clock.now()),
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
    worker: asFunction(
      ({ kafkaConsumer, messageProcessor, consumerConfig }: ConsumerCradle) =>
        new KafkaConsumerWorker({
          client: kafkaConsumer,
          topics: [ORDER_EVENTS_TOPIC],
          handler: messageProcessor,
          groupId: consumerConfig.groupId,
        }),
    ).singleton(),
  };
  container.register(registrations);
  instrumentUseCases(container, CONSUMER_USE_CASES);
  return container;
};
