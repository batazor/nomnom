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
import { OtelConsumerMetrics } from "../pkg/infrastructure/messaging/metrics.ts";
import {
  type ConsumerMetrics,
  type EventHandler,
  type Inbox,
  MessageProcessor,
} from "../pkg/infrastructure/messaging/processor.ts";
import type { MongoDbConnection } from "../pkg/infrastructure/mongodb/client.ts";
import { MongoInbox } from "../pkg/infrastructure/mongodb/inbox.ts";
import { MongoUnitOfWork } from "../pkg/infrastructure/mongodb/unit-of-work.ts";
import type { MessagePublisher } from "../pkg/infrastructure/outbox/relay.ts";
import type { Worker } from "../pkg/lifecycle/lifecycle.ts";
import type { RestaurantRepository } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import {
  createRestaurantCollection,
  MongoRestaurantRepository,
} from "../slices/restaurant/infrastructure/mongodb/restaurants.ts";
import type { KitchenAccessControl } from "../slices/restaurantorder/applications/access-control.ts";
import { ReceiveOrderRequestHandler } from "../slices/restaurantorder/applications/receive-order-request/receive-order-request.ts";
import type { RestaurantOrderRepository } from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
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

/** Configuration and resources the composition root hands to the container. */
export type ConsumerResources = Readonly<{
  consumer: ConsumerConfig;
  mongo: MongoDbConnection;
  /** Not yet connected; the worker connects and disconnects it. */
  kafkaConsumer: ConsumerClient;
  /** Producer of `<stream>.dlq` dead letters. */
  deadLetters: MessagePublisher;
  log: (message: string) => void;
}>;

export interface ConsumerCradle {
  readonly consumerConfig: ConsumerConfig;
  readonly mongoClient: MongoClient;
  readonly db: Db;
  readonly kafkaConsumer: ConsumerClient;
  readonly deadLetters: MessagePublisher;
  readonly log: (message: string) => void;
  readonly clock: Clock;
  readonly ids: IdGenerator;
  readonly unitOfWork: MongoUnitOfWork;
  readonly restaurants: RestaurantRepository;
  readonly restaurantOrders: RestaurantOrderRepository;
  /** Allows only Receive Order Request from the orders event stream. */
  readonly kitchenAccessControl: KitchenAccessControl;
  readonly receiveOrderRequest: ReceiveOrderRequestHandler;
  readonly inbox: Inbox;
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
    log: asValue(resources.log),
    clock: asValue<Clock>({ now: () => new Date() }),
    ids: asValue<IdGenerator>({ next: () => randomUUID() }),
    unitOfWork: asFunction(
      ({ mongoClient }: ConsumerCradle) => new MongoUnitOfWork(mongoClient),
    ).singleton(),
    restaurants: asFunction(
      ({ db, unitOfWork }: ConsumerCradle) =>
        new MongoRestaurantRepository(
          createRestaurantCollection(db, unitOfWork),
        ),
    ).singleton(),
    restaurantOrders: asFunction(
      ({ db, unitOfWork }: ConsumerCradle) =>
        new MongoRestaurantOrderRepository(
          createRestaurantOrderCollection(db, unitOfWork),
        ),
    ).singleton(),
    kitchenAccessControl: asFunction(
      () => new OrdersEventSourceAccessControl(),
    ).singleton(),
    receiveOrderRequest: asFunction(
      ({
        kitchenAccessControl,
        restaurants,
        restaurantOrders,
        ids,
        clock,
      }: ConsumerCradle) =>
        new ReceiveOrderRequestHandler({
          accessControl: kitchenAccessControl,
          restaurants,
          restaurantOrders,
          ids,
          clock,
        }),
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
        log,
      }: ConsumerCradle) =>
        new MessageProcessor({
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
          log,
        }),
    ).singleton(),
    worker: asFunction(
      ({ kafkaConsumer, messageProcessor }: ConsumerCradle) =>
        new KafkaConsumerWorker({
          client: kafkaConsumer,
          topics: [ORDER_EVENTS_TOPIC],
          handler: messageProcessor,
        }),
    ).singleton(),
  };
  container.register(registrations);
  return container;
};
