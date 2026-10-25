import assert from "node:assert/strict";
import test from "node:test";

import { InMemoryInbox } from "../pkg/infrastructure/memory/inbox.ts";
import { InMemoryUnitOfWork } from "../pkg/infrastructure/memory/unit-of-work.ts";
import {
  type InboundMessage,
  PermanentMessageError,
  parseEnvelope,
} from "../pkg/infrastructure/messaging/envelope.ts";
import {
  type ConsumerMetrics,
  type ErrorClass,
  type EventHandler,
  MessageProcessor,
} from "../pkg/infrastructure/messaging/processor.ts";
import type { IntegrationMessage } from "../pkg/infrastructure/outbox/relay.ts";

const TOPIC = "orders.v1.order-events";
const EVENT_TYPE = "nomnom.orders.v1.OrderPlaced";

const message = (
  headers: Record<string, string> = {},
  value = new Uint8Array([1, 2, 3]),
): InboundMessage => ({
  topic: TOPIC,
  partition: 1,
  offset: "7",
  key: Buffer.from("order-1"),
  value,
  headers: {
    event_id: "event-1",
    event_type: EVENT_TYPE,
    occurred_at: "2026-10-25T09:00:00.123456789Z",
    aggregate_type: "order",
    aggregate_id: "order-1",
    aggregate_version: "1",
    ...headers,
  },
});

class Recorder implements ConsumerMetrics {
  readonly events: string[] = [];
  processed(_l: unknown, _d: number, errorClass?: string) {
    this.events.push(`processed:${errorClass ?? "ok"}`);
  }
  retried() {
    this.events.push("retried");
  }
  poisoned(_l: unknown, errorClass: string) {
    this.events.push(`poisoned:${errorClass}`);
  }
  duplicate() {
    this.events.push("duplicate");
  }
  messageAge() {}
}

/** Handler whose `handle` runs the given steps, one per call. */
const scripted = (
  steps: Array<() => Promise<void>>,
  classify: (error: unknown) => ErrorClass | undefined = () => undefined,
) => {
  const calls: Uint8Array[] = [];
  const handler: EventHandler = {
    eventType: EVENT_TYPE,
    async handle(_envelope, payload) {
      calls.push(payload);
      const step = steps[Math.min(calls.length - 1, steps.length - 1)];
      await step?.();
    },
    classify,
  };
  return { handler, calls };
};

const setup = (handler: EventHandler, maxRetries = 2) => {
  const unitOfWork = new InMemoryUnitOfWork();
  const inbox = new InMemoryInbox(unitOfWork);
  const deadLetters: IntegrationMessage[] = [];
  const metrics = new Recorder();
  const processor = new MessageProcessor({
    consumer: "restaurants.order-placed",
    group: "restaurants",
    handlers: [handler],
    inbox,
    deadLetters: {
      publish: async (dead) => {
        deadLetters.push(dead);
      },
    },
    retry: { maxRetries, initialIntervalMs: 1, maxIntervalMs: 2 },
    metrics,
    log: () => {},
  });
  return { processor, inbox, deadLetters, metrics, unitOfWork };
};

test("parseEnvelope reads the orders Event Envelope headers", () => {
  const envelope = parseEnvelope({
    ...message().headers,
    traceparent: "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
  });

  assert.equal(envelope.eventId, "event-1");
  assert.equal(envelope.eventType, EVENT_TYPE);
  assert.equal(envelope.occurredAt.toISOString(), "2026-10-25T09:00:00.123Z");
  assert.equal(envelope.aggregateId, "order-1");
  assert.equal(envelope.aggregateVersion, 1);
  assert.ok(envelope.traceparent);
});

test("parseEnvelope rejects a missing event_id or a malformed occurred_at", () => {
  assert.throws(
    () => parseEnvelope({ ...message().headers, event_id: "" }),
    PermanentMessageError,
  );
  assert.throws(
    () => parseEnvelope({ ...message().headers, occurred_at: "yesterday" }),
    PermanentMessageError,
  );
  assert.throws(
    () => parseEnvelope({ ...message().headers, aggregate_version: "-1" }),
    PermanentMessageError,
  );
});

test("ADR 11: a redelivered event_id is a duplicate and the handler runs once", async () => {
  const { handler, calls } = scripted([async () => {}]);
  const { processor, inbox, metrics } = setup(handler);

  assert.equal(await processor.process(message()), "processed");
  assert.equal(await processor.process(message()), "duplicate");

  assert.equal(calls.length, 1);
  assert.deepEqual(
    inbox.records.map((record) => [record.consumer, record.eventId]),
    [["restaurants.order-placed", "event-1"]],
  );
  assert.deepEqual(metrics.events, [
    "processed:ok",
    "duplicate",
    "processed:ok",
  ]);
});

test("an event type the consumer does not handle is acknowledged without the Inbox", async () => {
  const { handler, calls } = scripted([async () => {}]);
  const { processor, inbox } = setup(handler);

  const outcome = await processor.process(
    message({ event_type: "nomnom.orders.v1.OrderCompleted" }),
  );

  assert.equal(outcome, "ignored");
  assert.equal(calls.length, 0);
  assert.equal(inbox.records.length, 0);
});

test("a broken envelope goes to <stream>.dlq with poison metadata", async () => {
  const { handler, calls } = scripted([async () => {}]);
  const { processor, deadLetters, metrics } = setup(handler);

  const outcome = await processor.process(message({ event_id: "" }));

  assert.equal(outcome, "dead-lettered");
  assert.equal(calls.length, 0);
  assert.equal(deadLetters.length, 1);
  const [dead] = deadLetters;
  assert.equal(dead?.topic, `${TOPIC}.dlq`);
  assert.equal(dead?.key, "order-1");
  assert.deepEqual(dead?.value, new Uint8Array([1, 2, 3]));
  assert.equal(dead?.headers.topic_poisoned, TOPIC);
  assert.equal(dead?.headers.consumer_group_poisoned, "restaurants");
  assert.match(dead?.headers.reason_poisoned ?? "", /event_id/);
  assert.deepEqual(metrics.events, [
    "poisoned:deserialization",
    "processed:deserialization",
  ]);
});

test("a permanent handler error is dead-lettered and the Inbox change rolls back", async () => {
  const { handler } = scripted([
    async () => {
      throw new PermanentMessageError("no items");
    },
  ]);
  const { processor, inbox, deadLetters } = setup(handler);

  assert.equal(await processor.process(message()), "dead-lettered");

  assert.equal(deadLetters.length, 1);
  assert.equal(inbox.records.length, 0, "Inbox record rolled back");
});

test("a business outcome is acknowledged without dead letter or Inbox record", async () => {
  const { handler } = scripted(
    [
      async () => {
        throw Object.assign(new Error("restaurant missing"), {
          code: "not_found",
        });
      },
    ],
    () => "business",
  );
  const { processor, inbox, deadLetters, metrics } = setup(handler);

  assert.equal(await processor.process(message()), "rejected");

  assert.equal(deadLetters.length, 0);
  assert.equal(inbox.records.length, 0);
  assert.deepEqual(metrics.events, ["processed:business"]);
});

test("a transient error is retried in process and then succeeds", async () => {
  const { handler, calls } = scripted([
    async () => {
      throw new Error("mongo unavailable");
    },
    async () => {},
  ]);
  const { processor, inbox, metrics } = setup(handler);

  assert.equal(await processor.process(message()), "processed");

  assert.equal(calls.length, 2);
  assert.equal(inbox.records.length, 1);
  assert.deepEqual(metrics.events, ["retried", "processed:ok"]);
});

test("exhausted transient retries reject so that Kafka redelivers the message", async () => {
  const { handler, calls } = scripted([
    async () => {
      throw new Error("mongo unavailable");
    },
  ]);
  const { processor, inbox, deadLetters } = setup(handler, 2);

  await assert.rejects(processor.process(message()), /mongo unavailable/);

  assert.equal(calls.length, 3, "first attempt and two retries");
  assert.equal(inbox.records.length, 0);
  assert.equal(deadLetters.length, 0);
});

test("an aborted signal stops retries", async () => {
  const { handler, calls } = scripted([
    async () => {
      throw new Error("mongo unavailable");
    },
  ]);
  const { processor } = setup(handler, 10);
  const abort = new AbortController();
  abort.abort();

  await assert.rejects(processor.process(message(), abort.signal));

  assert.equal(calls.length, 1);
});

test("a failed dead letter publish rejects, so the message is not lost", async () => {
  const { handler } = scripted([async () => {}]);
  const processor = new MessageProcessor({
    consumer: "restaurants.order-placed",
    group: "restaurants",
    handlers: [handler],
    inbox: new InMemoryInbox(new InMemoryUnitOfWork()),
    deadLetters: {
      publish: async () => {
        throw new Error("broker down");
      },
    },
    retry: { maxRetries: 0, initialIntervalMs: 1, maxIntervalMs: 1 },
    log: () => {},
  });

  await assert.rejects(
    processor.process(message({ event_type: "" })),
    /broker down/,
  );
});
