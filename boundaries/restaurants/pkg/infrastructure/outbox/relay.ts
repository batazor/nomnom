// Polling relay of the Transactional Outbox (ADR 13, ADR 31): single active
// instance by lease, batch in `seq` order, publication confirmed by the broker
// before `publishedAt` is set, head-of-line stop at the first failure and
// retention of published records. Storage, Kafka and metrics are ports.

/** Unpublished Outbox record as the relay sees it. */
export type RelayRecord = Readonly<{
  id: string;
  seq: number;
  eventId: string;
  eventType: string;
  aggregateType: string;
  aggregateId: string;
  aggregateVersion: number;
  occurredAt: Date;
  topic: string;
  payload: Uint8Array;
  metadata: Readonly<{ traceparent?: string; causationId?: string }>;
  attempts: number;
}>;

/** Backlog of one stream (topic). */
export type PendingStats = Readonly<{
  stream: string;
  pending: number;
  oldestOccurredAt: Date | undefined;
}>;

export interface RelayStore {
  /**
   * Takes or renews the lease; `false` while another owner holds it. `now` is
   * the relay's clock; a store with a server clock (MongoDB `$$NOW`) MAY
   * ignore it so that clock skew between relays cannot steal a live lease.
   */
  acquireLease(owner: string, now: Date, ttlMs: number): Promise<boolean>;
  releaseLease(owner: string): Promise<void>;
  /** Unpublished records ordered by `seq`. */
  fetchPending(limit: number): Promise<readonly RelayRecord[]>;
  markPublished(id: string, publishedAt: Date): Promise<void>;
  markFailed(id: string, error: string): Promise<void>;
  /** Deletes published records older than `cutoff`; returns the count. */
  deletePublishedBefore(cutoff: Date): Promise<number>;
  pendingStats(): Promise<readonly PendingStats[]>;
}

/** Kafka message of an Integration Event. */
export type IntegrationMessage = Readonly<{
  topic: string;
  key: string;
  value: Uint8Array;
  headers: Readonly<Record<string, string>>;
}>;

export interface MessagePublisher {
  /** Resolves only after the broker acknowledged the message (acks=all). */
  publish(message: IntegrationMessage): Promise<void>;
}

export interface RelayMetrics {
  published(record: RelayRecord, durationS: number, lagS: number): void;
  failed(record: RelayRecord, errorClass: string): void;
  /** Current backlog; an empty list while this instance has no lease. */
  backlog(stats: readonly PendingStats[]): void;
}

export const noRelayMetrics: RelayMetrics = {
  published: () => {},
  failed: () => {},
  backlog: () => {},
};

/**
 * Event Envelope headers, the same format as orders
 * (pkg/messaging/envelope.go): `_watermill_message_uuid` is the Watermill
 * message UUID, which the orders publisher also sets to `event_id`.
 */
export function envelopeHeaders(record: RelayRecord): Record<string, string> {
  const headers: Record<string, string> = {
    _watermill_message_uuid: record.eventId,
    event_id: record.eventId,
    event_type: record.eventType,
    occurred_at: record.occurredAt
      .toISOString()
      .replace(/\.(\d{3})Z$/, (_m, ms: string) => {
        const trimmed = ms.replace(/0+$/, "");
        return trimmed === "" ? "Z" : `.${trimmed}Z`;
      }),
    aggregate_type: record.aggregateType,
    aggregate_id: record.aggregateId,
    aggregate_version: String(record.aggregateVersion),
  };
  if (record.metadata.traceparent) {
    headers.traceparent = record.metadata.traceparent;
  }
  return headers;
}

export const toIntegrationMessage = (
  record: RelayRecord,
): IntegrationMessage => ({
  topic: record.topic,
  key: record.aggregateId,
  value: record.payload,
  headers: envelopeHeaders(record),
});

export type CycleResult = Readonly<{
  leased: boolean;
  published: number;
  /** Record that stopped the batch (head-of-line). */
  failedId?: string;
  deleted: number;
}>;

export interface OutboxRelayOptions {
  store: RelayStore;
  publisher: MessagePublisher;
  owner: string;
  batchSize: number;
  leaseTtlMs: number;
  retentionMs: number;
  /** Minimum time between retention passes. */
  cleanupIntervalMs?: number;
  metrics?: RelayMetrics;
  clock?: { now(): Date };
  log?: (message: string) => void;
}

const DEFAULT_CLEANUP_INTERVAL_MS = 60_000;

/**
 * Part of the lease TTL after which the relay renews the lease before the
 * next record. With a publish bounded by {@link relayMessageTimeoutMs} (at
 * most a third of the TTL as well), a record is never published after the
 * lease could have expired and been taken over by a standby relay.
 */
const LEASE_RENEW_FRACTION = 3;

/**
 * `message.timeout.ms` of the relay producer: a third of the lease TTL and of
 * the drain timeout, so a stuck broker neither outlives the lease nor delays
 * SIGTERM beyond the drain (librdkafka defaults to 300 s).
 */
export const relayMessageTimeoutMs = (
  leaseTtlMs: number,
  drainTimeoutMs: number,
): number =>
  Math.max(
    1000,
    Math.floor(Math.min(leaseTtlMs, drainTimeoutMs) / LEASE_RENEW_FRACTION),
  );

export class OutboxRelay {
  readonly #options: OutboxRelayOptions;
  readonly #metrics: RelayMetrics;
  readonly #clock: { now(): Date };
  #lastCleanup: number | undefined;
  #leased = false;
  /** `performance.now()` when the current lease was last confirmed. */
  #renewedAt = 0;

  constructor(options: OutboxRelayOptions) {
    this.#options = options;
    this.#metrics = options.metrics ?? noRelayMetrics;
    this.#clock = options.clock ?? { now: () => new Date() };
  }

  /** One polling cycle; stops early when `signal` aborts. */
  async cycle(signal?: AbortSignal): Promise<CycleResult> {
    const { store } = this.#options;
    if (!(await this.#renewLease())) {
      this.#metrics.backlog([]);
      return { leased: false, published: 0, deleted: 0 };
    }

    let published = 0;
    let failedId: string | undefined;
    const batch = await store.fetchPending(this.#options.batchSize);
    for (const record of batch) {
      if (signal?.aborted) break;
      // A slow batch (broker retries) renews the lease before the next
      // record; a lost lease stops the batch so the new owner keeps order.
      if (
        performance.now() - this.#renewedAt >=
          this.#options.leaseTtlMs / LEASE_RENEW_FRACTION &&
        !(await this.#renewLease())
      ) {
        this.#metrics.backlog([]);
        return { leased: false, published, deleted: 0 };
      }
      const started = performance.now();
      try {
        await this.#options.publisher.publish(toIntegrationMessage(record));
      } catch (error) {
        // Head-of-line: the next cycle starts again from this record.
        failedId = record.id;
        this.#metrics.failed(record, errorClass(error));
        await store.markFailed(record.id, describe(error));
        this.#options.log?.(
          `publish ${record.eventType} ${record.eventId} failed (attempt ${record.attempts + 1}): ${describe(error)}`,
        );
        break;
      }
      const acknowledgedAt = this.#clock.now();
      await store.markPublished(record.id, acknowledgedAt);
      published += 1;
      this.#metrics.published(
        record,
        (performance.now() - started) / 1000,
        Math.max(0, acknowledgedAt.getTime() - record.occurredAt.getTime()) /
          1000,
      );
    }

    const deleted = await this.#cleanup();
    this.#metrics.backlog(await store.pendingStats());
    return failedId === undefined
      ? { leased: true, published, deleted }
      : { leased: true, published, failedId, deleted };
  }

  /** Takes or renews the lease and remembers when it was confirmed. */
  async #renewLease(): Promise<boolean> {
    const { store, owner } = this.#options;
    const requestedAt = performance.now();
    const leased = await store.acquireLease(
      owner,
      this.#clock.now(),
      this.#options.leaseTtlMs,
    );
    if (leased !== this.#leased) {
      this.#options.log?.(
        leased ? `lease acquired by ${owner}` : `lease held by another relay`,
      );
      this.#leased = leased;
    }
    // The server computed the expiry no earlier than the request was sent.
    if (leased) this.#renewedAt = requestedAt;
    return leased;
  }

  /** Gives the lease up so a standby relay takes over without waiting. */
  async release(): Promise<void> {
    if (!this.#leased) return;
    this.#leased = false;
    await this.#options.store.releaseLease(this.#options.owner);
  }

  async #cleanup(): Promise<number> {
    const now = this.#clock.now();
    const interval =
      this.#options.cleanupIntervalMs ?? DEFAULT_CLEANUP_INTERVAL_MS;
    if (
      this.#lastCleanup !== undefined &&
      now.getTime() - this.#lastCleanup < interval
    ) {
      return 0;
    }
    this.#lastCleanup = now.getTime();
    return this.#options.store.deletePublishedBefore(
      new Date(now.getTime() - this.#options.retentionMs),
    );
  }
}

/** Closed set of `error_class` label values. */
export function errorClass(error: unknown): string {
  if (!(error instanceof Error)) return "unknown";
  const name = `${error.name} ${(error as { code?: unknown }).code ?? ""}`;
  if (/timeout|timed_out/i.test(name) || /timed out/i.test(error.message)) {
    return "timeout";
  }
  if (/kafka/i.test(name)) return "broker";
  return "unknown";
}

const LAST_ERROR_MAX = 1024;

const describe = (error: unknown): string => {
  const text = error instanceof Error ? error.message : String(error);
  return text.length > LAST_ERROR_MAX ? text.slice(0, LAST_ERROR_MAX) : text;
};
