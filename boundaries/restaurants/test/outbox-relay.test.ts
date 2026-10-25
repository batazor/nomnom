import assert from "node:assert/strict";
import test from "node:test";

import {
  envelopeHeaders,
  errorClass,
  type IntegrationMessage,
  OutboxRelay,
  type PendingStats,
  type RelayMetrics,
  type RelayRecord,
  type RelayStore,
  relayMessageTimeoutMs,
} from "../pkg/infrastructure/outbox/relay.ts";

type StoredRecord = RelayRecord & {
  publishedAt: Date | null;
  lastError?: string;
};

class MemoryRelayStore implements RelayStore {
  records: StoredRecord[] = [];
  lease: { owner: string; expiresAt: Date } | undefined;

  async acquireLease(owner: string, now: Date, ttlMs: number) {
    if (
      this.lease !== undefined &&
      this.lease.owner !== owner &&
      this.lease.expiresAt > now
    ) {
      return false;
    }
    this.lease = { owner, expiresAt: new Date(now.getTime() + ttlMs) };
    return true;
  }
  async releaseLease(owner: string) {
    if (this.lease?.owner === owner) this.lease = undefined;
  }
  async fetchPending(limit: number) {
    return this.records
      .filter((record) => record.publishedAt === null)
      .sort((a, b) => a.seq - b.seq)
      .slice(0, limit);
  }
  #update(id: string, change: (record: StoredRecord) => StoredRecord) {
    this.records = this.records.map((record) =>
      record.id === id ? change(record) : record,
    );
  }
  async markPublished(id: string, publishedAt: Date) {
    this.#update(id, (record) => ({ ...record, publishedAt }));
  }
  async markFailed(id: string, error: string) {
    this.#update(id, (record) => ({
      ...record,
      attempts: record.attempts + 1,
      lastError: error,
    }));
  }
  async deletePublishedBefore(cutoff: Date) {
    const before = this.records.length;
    this.records = this.records.filter(
      (record) => record.publishedAt === null || record.publishedAt >= cutoff,
    );
    return before - this.records.length;
  }
  async pendingStats(): Promise<PendingStats[]> {
    const pending = this.records.filter(
      (record) => record.publishedAt === null,
    );
    if (pending.length === 0) return [];
    return [
      {
        stream: "restaurants.v1.restaurant-order-events",
        pending: pending.length,
        oldestOccurredAt: pending[0]?.occurredAt,
      },
    ];
  }
}

const record = (
  seq: number,
  overrides: Partial<StoredRecord> = {},
): StoredRecord => ({
  id: `id-${seq}`,
  seq,
  eventId: `event-${seq}`,
  eventType: "nomnom.restaurants.v1.PreparationStarted",
  aggregateType: "RestaurantOrder",
  aggregateId: `ro-${seq}`,
  aggregateVersion: seq,
  occurredAt: new Date(Date.UTC(2026, 9, 25, 9, 0, seq)),
  topic: "restaurants.v1.restaurant-order-events",
  payload: new Uint8Array([seq]),
  metadata: {},
  attempts: 0,
  publishedAt: null,
  ...overrides,
});

class FakePublisher {
  sent: IntegrationMessage[] = [];
  failOn = new Set<string>();
  async publish(message: IntegrationMessage) {
    if (this.failOn.has(message.headers.event_id ?? "")) {
      const error = new Error("Broker: Request timed out");
      error.name = "KafkaJSTimeout";
      throw error;
    }
    this.sent.push(message);
  }
}

const recordingMetrics = () => {
  const calls = {
    published: 0,
    failed: [] as string[],
    backlog: [] as PendingStats[][],
  };
  const metrics: RelayMetrics = {
    published: () => {
      calls.published += 1;
    },
    failed: (_record, errorClassValue) => calls.failed.push(errorClassValue),
    backlog: (stats) => calls.backlog.push([...stats]),
  };
  return { calls, metrics };
};

const now = new Date(Date.UTC(2026, 9, 25, 10));
const relayFor = (
  store: MemoryRelayStore,
  publisher: FakePublisher,
  options: {
    owner?: string;
    metrics?: RelayMetrics;
    clock?: { now(): Date };
  } = {},
) =>
  new OutboxRelay({
    store,
    publisher,
    owner: options.owner ?? "relay-a",
    batchSize: 10,
    leaseTtlMs: 15_000,
    retentionMs: 7 * 86_400_000,
    clock: options.clock ?? { now: () => now },
    ...(options.metrics === undefined ? {} : { metrics: options.metrics }),
  });

test("publishes pending records in seq order and marks them after the ack", async () => {
  const store = new MemoryRelayStore();
  store.records = [record(3), record(1), record(2, { publishedAt: now })];
  const publisher = new FakePublisher();
  const { calls, metrics } = recordingMetrics();

  const result = await relayFor(store, publisher, { metrics }).cycle();

  assert.deepEqual(result, { leased: true, published: 2, deleted: 0 });
  assert.deepEqual(
    publisher.sent.map((message) => message.headers.event_id),
    ["event-1", "event-3"],
  );
  assert.equal(publisher.sent[0]?.key, "ro-1");
  assert.ok(store.records.every((stored) => stored.publishedAt !== null));
  assert.equal(calls.published, 2);
  assert.deepEqual(calls.backlog.at(-1), []);
});

test("head-of-line: the first failure stops the batch and is retried first", async () => {
  const store = new MemoryRelayStore();
  store.records = [record(1), record(2), record(3)];
  const publisher = new FakePublisher();
  publisher.failOn.add("event-2");
  const { calls, metrics } = recordingMetrics();
  const relay = relayFor(store, publisher, { metrics });

  const first = await relay.cycle();
  assert.deepEqual(first, {
    leased: true,
    published: 1,
    failedId: "id-2",
    deleted: 0,
  });
  assert.deepEqual(
    publisher.sent.map((message) => message.headers.event_id),
    ["event-1"],
  );
  const failed = store.records.find((stored) => stored.id === "id-2");
  assert.equal(failed?.attempts, 1);
  assert.equal(failed?.lastError, "Broker: Request timed out");
  assert.equal(store.records.find((s) => s.id === "id-3")?.publishedAt, null);
  assert.deepEqual(calls.failed, ["timeout"]);
  assert.equal(calls.backlog.at(-1)?.[0]?.pending, 2);

  await relay.cycle();
  assert.equal(store.records.find((s) => s.id === "id-2")?.attempts, 2);

  publisher.failOn.clear();
  const recovered = await relay.cycle();
  assert.equal(recovered.published, 2);
  assert.deepEqual(
    publisher.sent.map((message) => message.headers.event_id),
    ["event-1", "event-2", "event-3"],
  );
});

test("only the lease owner reads the Outbox; an expired lease fails over", async () => {
  const store = new MemoryRelayStore();
  store.records = [record(1)];
  let current = now;
  const clock = { now: () => current };
  const publisherA = new FakePublisher();
  const publisherB = new FakePublisher();
  const a = relayFor(store, publisherA, { owner: "a", clock });
  const b = relayFor(store, publisherB, { owner: "b", clock });
  const { calls, metrics } = recordingMetrics();
  const bWithMetrics = relayFor(store, publisherB, {
    owner: "b",
    clock,
    metrics,
  });

  assert.equal((await a.cycle()).leased, true);
  store.records.push(record(2));
  assert.deepEqual(await b.cycle(), {
    leased: false,
    published: 0,
    deleted: 0,
  });
  await bWithMetrics.cycle();
  assert.deepEqual(calls.backlog, [[]]);
  assert.equal(publisherB.sent.length, 0);

  // a stops renewing; after the TTL b takes over.
  current = new Date(now.getTime() + 15_001);
  const takeover = await b.cycle();
  assert.equal(takeover.leased, true);
  assert.deepEqual(
    publisherB.sent.map((message) => message.headers.event_id),
    ["event-2"],
  );
  assert.equal((await a.cycle()).leased, false);
});

test("release gives the lease up for a standby relay", async () => {
  const store = new MemoryRelayStore();
  const a = relayFor(store, new FakePublisher(), { owner: "a" });
  const b = relayFor(store, new FakePublisher(), { owner: "b" });
  await a.cycle();
  assert.equal((await b.cycle()).leased, false);
  await a.release();
  assert.equal((await b.cycle()).leased, true);
});

test("retention deletes only published records older than OUTBOX__RETENTION", async () => {
  const store = new MemoryRelayStore();
  const old = new Date(now.getTime() - 8 * 86_400_000);
  const recent = new Date(now.getTime() - 86_400_000);
  store.records = [
    record(1, { publishedAt: old }),
    record(2, { publishedAt: recent }),
    record(3, { occurredAt: old }),
  ];
  const publisher = new FakePublisher();
  publisher.failOn.add("event-3");

  const result = await relayFor(store, publisher).cycle();

  assert.equal(result.deleted, 1);
  assert.deepEqual(
    store.records.map((stored) => stored.id),
    ["id-2", "id-3"],
  );
});

test("envelope headers match the orders format", () => {
  const headers = envelopeHeaders(
    record(7, {
      occurredAt: new Date("2026-10-25T09:00:00.120Z"),
      metadata: {
        traceparent: "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
      },
    }),
  );
  assert.deepEqual(headers, {
    _watermill_message_uuid: "event-7",
    event_id: "event-7",
    event_type: "nomnom.restaurants.v1.PreparationStarted",
    occurred_at: "2026-10-25T09:00:00.12Z",
    aggregate_type: "RestaurantOrder",
    aggregate_id: "ro-7",
    aggregate_version: "7",
    traceparent: "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
  });
  // Go time.RFC3339Nano drops a zero fraction entirely.
  assert.equal(
    envelopeHeaders(
      record(1, { occurredAt: new Date("2026-10-25T09:00:00.000Z") }),
    ).occurred_at,
    "2026-10-25T09:00:00Z",
  );
  assert.equal("traceparent" in envelopeHeaders(record(1)), false);
});

test("error classes are a closed set", () => {
  const kafkaError = new Error("broker down");
  kafkaError.name = "KafkaJSConnectionError";
  assert.equal(errorClass(kafkaError), "broker");
  assert.equal(errorClass(new Error("Request timed out")), "timeout");
  assert.equal(errorClass("oops"), "unknown");
  assert.equal(errorClass(new TypeError("x")), "unknown");
});

/** Publisher whose every publish takes `delayMs`, then runs `onSent`. */
class SlowPublisher extends FakePublisher {
  readonly delayMs: number;
  readonly onSent: (message: IntegrationMessage) => void;

  constructor(
    delayMs: number,
    onSent: (message: IntegrationMessage) => void = () => {},
  ) {
    super();
    this.delayMs = delayMs;
    this.onSent = onSent;
  }

  override async publish(message: IntegrationMessage) {
    await new Promise((resolve) => setTimeout(resolve, this.delayMs));
    await super.publish(message);
    this.onSent(message);
  }
}

const slowRelay = (
  store: MemoryRelayStore,
  publisher: FakePublisher,
  leaseTtlMs: number,
) =>
  new OutboxRelay({
    store,
    publisher,
    owner: "relay-a",
    batchSize: 10,
    leaseTtlMs,
    retentionMs: 7 * 86_400_000,
    // Real clock: the lease expiry and the renewal deadline move together.
    clock: { now: () => new Date() },
  });

test("a slow batch renews the lease before the next record", async () => {
  const store = new MemoryRelayStore();
  store.records = [record(1), record(2), record(3)];
  let acquisitions = 0;
  const acquire = store.acquireLease.bind(store);
  store.acquireLease = async (...args) => {
    acquisitions += 1;
    return acquire(...args);
  };
  const publisher = new SlowPublisher(40);

  const result = await slowRelay(store, publisher, 90).cycle();

  assert.deepEqual(result, { leased: true, published: 3, deleted: 0 });
  assert.ok(acquisitions >= 3, `renewed during the batch (${acquisitions})`);
  assert.ok(store.lease !== undefined && store.lease.expiresAt > new Date());
});

test("a lease lost during a slow batch stops it before the next record", async () => {
  const store = new MemoryRelayStore();
  store.records = [record(1), record(2), record(3)];
  // While record 1 is in flight the lease expires and relay-b takes it.
  const publisher = new SlowPublisher(40, () => {
    store.lease = {
      owner: "relay-b",
      expiresAt: new Date(Date.now() + 60_000),
    };
  });
  const { calls, metrics } = recordingMetrics();
  const relay = new OutboxRelay({
    store,
    publisher,
    owner: "relay-a",
    batchSize: 10,
    leaseTtlMs: 90,
    retentionMs: 7 * 86_400_000,
    clock: { now: () => new Date() },
    metrics,
  });

  const result = await relay.cycle();

  assert.deepEqual(result, { leased: false, published: 1, deleted: 0 });
  assert.deepEqual(
    publisher.sent.map((message) => message.headers.event_id),
    ["event-1"],
    "the stale owner publishes nothing after losing the lease",
  );
  assert.equal(store.records.find((r) => r.id === "id-2")?.publishedAt, null);
  assert.deepEqual(calls.backlog.at(-1), [], "standby reports no backlog");
});

test("the relay message timeout stays below the lease TTL and the drain", () => {
  assert.equal(relayMessageTimeoutMs(15_000, 20_000), 5000);
  assert.equal(relayMessageTimeoutMs(30_000, 9000), 3000);
  assert.equal(relayMessageTimeoutMs(1500, 20_000), 1000, "floor of 1 s");
});
