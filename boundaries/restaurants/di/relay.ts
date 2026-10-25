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

/** Configuration and resources the composition root hands to the container. */
export type RelayResources = Readonly<{
  outbox: OutboxRelayConfig;
  db: Db;
  publisher: MessagePublisher;
  log: (message: string) => void;
}>;

export interface RelayCradle {
  readonly outboxConfig: OutboxRelayConfig;
  readonly db: Db;
  readonly publisher: MessagePublisher;
  readonly log: (message: string) => void;
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
    log: asValue(resources.log),
    relayStore: asFunction(
      ({ db }: RelayCradle) => new MongoOutboxRelayStore(db),
    ).singleton(),
    relayMetrics: asFunction(
      () => new OtelRelayMetrics("restaurants"),
    ).singleton(),
    outboxRelay: asFunction(
      ({
        relayStore,
        publisher,
        outboxConfig,
        relayMetrics,
        log,
      }: RelayCradle) =>
        new OutboxRelay({
          store: relayStore,
          publisher,
          owner: `${hostname()}/${process.pid}`,
          batchSize: outboxConfig.batchSize,
          leaseTtlMs: outboxConfig.leaseTtlMs,
          retentionMs: outboxConfig.retentionMs,
          metrics: relayMetrics,
          log,
        }),
    ).singleton(),
    worker: asFunction(
      ({ outboxRelay, outboxConfig }: RelayCradle) =>
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
                log: (message) => console.error(`restaurants-relay ${message}`),
              },
            ),
          ],
        ),
    ).singleton(),
  };
  container.register(registrations);
  return container;
};
