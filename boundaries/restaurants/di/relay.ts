// Composition root of `cmd/restaurants-relay` (ADR 5): Awilix registrations
// of the polling Outbox relay. Every registration lives in the application
// scope: the relay keeps its lease between cycles, and one cycle owns no
// resource beyond the batch it reads. The MongoDB connection and the Kafka
// producer are acquired and released by cmd/restaurants-relay.
import { hostname } from "node:os";

import { type AwilixContainer, asFunction, asValue } from "awilix";
import type { Db } from "mongodb";

import type { OutboxRelayConfig } from "../pkg/config/config.ts";
import {
  createProcessContainer,
  type Registrations,
} from "../pkg/di/container.ts";
import { MongoOutboxRelayStore } from "../pkg/infrastructure/mongodb/outbox-relay-store.ts";
import { OtelRelayMetrics } from "../pkg/infrastructure/outbox/metrics.ts";
import {
  type MessagePublisher,
  OutboxRelay,
  type RelayMetrics,
  type RelayStore,
} from "../pkg/infrastructure/outbox/relay.ts";
import { IdleWorker, type Worker } from "../pkg/lifecycle/lifecycle.ts";
import { PeriodicLoop, WorkerWithLoops } from "../pkg/lifecycle/periodic.ts";
import type { Logger } from "../pkg/telemetry/logger.ts";
import { MENU_EVENTS_TOPIC } from "../slices/menu/infrastructure/integration/menu-events.ts";
import { RESTAURANT_EVENTS_TOPIC } from "../slices/restaurant/infrastructure/integration/restaurant-events.ts";
import { RESTAURANT_ORDER_EVENTS_TOPIC } from "../slices/restaurantorder/infrastructure/integration/restaurant-order-events.ts";

/** Topics the restaurants Outbox writes; reported at 0 when empty. */
export const OUTBOX_TOPICS = [
  RESTAURANT_ORDER_EVENTS_TOPIC,
  RESTAURANT_EVENTS_TOPIC,
  MENU_EVENTS_TOPIC,
] as const;

/** Configuration and resources the composition root hands to the container. */
export type RelayResources = Readonly<{
  outbox: OutboxRelayConfig;
  db: Db;
  publisher: MessagePublisher;
  logger: Logger;
}>;

export interface RelayCradle {
  readonly outboxConfig: OutboxRelayConfig;
  readonly db: Db;
  readonly publisher: MessagePublisher;
  /** Structured stdout logs (pkg/telemetry/logger.ts). */
  readonly logger: Logger;
  readonly relayStore: RelayStore;
  readonly relayMetrics: RelayMetrics;
  readonly outboxRelay: OutboxRelay;
  /** Idle worker that releases the lease, with the polling loop. */
  readonly worker: Worker;
}

export const createRelayContainer = (
  resources: RelayResources,
): AwilixContainer<RelayCradle> => {
  const container = createProcessContainer<RelayCradle>();
  const registrations: Registrations<RelayCradle> = {
    outboxConfig: asValue(resources.outbox),
    db: asValue(resources.db),
    publisher: asValue(resources.publisher),
    logger: asValue(resources.logger),
    relayStore: asFunction(
      ({ db }: RelayCradle) => new MongoOutboxRelayStore(db),
    ).singleton(),
    relayMetrics: asFunction(
      () => new OtelRelayMetrics("restaurants", { knownTopics: OUTBOX_TOPICS }),
    ).singleton(),
    outboxRelay: asFunction(
      ({
        relayStore,
        publisher,
        outboxConfig,
        relayMetrics,
        logger,
      }: RelayCradle) =>
        new OutboxRelay({
          store: relayStore,
          publisher,
          owner: `${hostname()}/${process.pid}`,
          batchSize: outboxConfig.batchSize,
          leaseTtlMs: outboxConfig.leaseTtlMs,
          retentionMs: outboxConfig.retentionMs,
          metrics: relayMetrics,
          log: logger.child({ operation: "outbox.relay" }).info,
          warn: logger.child({ operation: "outbox.relay" }).warn,
        }),
    ).singleton(),
    worker: asFunction(
      ({ outboxRelay, outboxConfig, logger }: RelayCradle) =>
        new WorkerWithLoops(
          new IdleWorker(async () => {
            await outboxRelay.release();
          }),
          [
            new PeriodicLoop(
              async (signal) => {
                await outboxRelay.cycle(signal);
              },
              {
                initialDelayMs: 0,
                intervalMs: outboxConfig.pollIntervalMs,
                log: logger.child({ operation: "outbox.relay" }).error,
              },
            ),
          ],
        ),
    ).singleton(),
  };
  container.register(registrations);
  return container;
};
