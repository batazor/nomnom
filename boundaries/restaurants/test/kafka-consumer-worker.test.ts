import assert from "node:assert/strict";
import test from "node:test";

import {
  type ConsumerClient,
  type EachMessage,
  KafkaConsumerWorker,
  stringHeaders,
} from "../infrastructure/kafka/consumer.ts";
import type { InboundMessage } from "../infrastructure/messaging/envelope.ts";
import { Lifecycle } from "../pkg/lifecycle/lifecycle.ts";

/** Fake KafkaJS consumer: records calls and lets the test deliver messages. */
class FakeConsumer implements ConsumerClient {
  readonly calls: string[] = [];
  #eachMessage?: (payload: EachMessage) => Promise<void>;
  #inFlight = new Set<Promise<void>>();

  async connect() {
    this.calls.push("connect");
  }

  async subscribe(subscription: { topics: string[] }) {
    this.calls.push(`subscribe:${subscription.topics.join(",")}`);
  }

  async run(config: { eachMessage: (payload: EachMessage) => Promise<void> }) {
    this.calls.push("run");
    this.#eachMessage = config.eachMessage;
  }

  /** Like KafkaJS: a rejected eachMessage is not committed and is redelivered. */
  deliver(payload: EachMessage): Promise<"committed" | "redelivered"> {
    const handle = this.#eachMessage;
    assert.ok(handle, "run was called");
    const processing = handle(payload);
    const tracked = processing.then(
      () => {},
      () => {},
    );
    this.#inFlight.add(tracked);
    return processing.then(
      () => "committed" as const,
      () => "redelivered" as const,
    );
  }

  async disconnect() {
    // Waits for in-flight eachMessage, as the library does.
    await Promise.all(this.#inFlight);
    this.calls.push("disconnect");
  }
}

const payload = (headers: EachMessage["message"]["headers"]): EachMessage => ({
  topic: "orders.v1.order-events",
  partition: 2,
  message: {
    key: Buffer.from("order-1"),
    value: Buffer.from([1, 2]),
    offset: "42",
    headers,
  },
});

test("stringHeaders keeps the first value of repeated headers as strings", () => {
  assert.deepEqual(
    stringHeaders({
      event_id: Buffer.from("e-1"),
      event_type: "nomnom.orders.v1.OrderPlaced",
      repeated: [Buffer.from("first"), "second"],
      missing: undefined,
    }),
    {
      event_id: "e-1",
      event_type: "nomnom.orders.v1.OrderPlaced",
      repeated: "first",
    },
  );
});

test("the worker subscribes, maps Kafka messages and commits only processed ones", async () => {
  const client = new FakeConsumer();
  const received: InboundMessage[] = [];
  let fail = false;
  const worker = new KafkaConsumerWorker({
    client,
    topics: ["orders.v1.order-events"],
    handler: {
      process: async (message) => {
        received.push(message);
        if (fail) throw new Error("transient");
        return "processed";
      },
    },
  });

  await worker.start();
  assert.deepEqual(client.calls, [
    "connect",
    "subscribe:orders.v1.order-events",
    "run",
  ]);
  assert.equal(
    await client.deliver(payload({ event_id: Buffer.from("e-1") })),
    "committed",
  );
  fail = true;
  assert.equal(await client.deliver(payload({})), "redelivered");

  const [first] = received;
  assert.equal(first?.topic, "orders.v1.order-events");
  assert.equal(first?.partition, 2);
  assert.equal(first?.offset, "42");
  assert.equal(Buffer.from(first?.key ?? []).toString(), "order-1");
  assert.deepEqual(first?.headers, { event_id: "e-1" });

  const running = worker.run();
  await worker.shutdown(AbortSignal.timeout(1_000));
  await running;
  assert.equal(client.calls.at(-1), "disconnect");
});

test("SIGTERM drains the message in progress before disconnect (runtime contract)", async () => {
  const client = new FakeConsumer();
  let release: () => void = () => {};
  const worker = new KafkaConsumerWorker({
    client,
    topics: ["orders.v1.order-events"],
    handler: {
      process: () =>
        new Promise((done) => {
          release = () => done("processed");
        }),
    },
  });
  const stop = new AbortController();
  const running = new Lifecycle({
    managementPort: 0,
    readinessDelayMs: 0,
    drainTimeoutMs: 1_000,
  }).run(worker, { signal: stop.signal, log: () => {} });
  while (!client.calls.includes("run")) {
    await new Promise((tick) => setImmediate(tick));
  }
  const delivered = client.deliver(payload({}));

  stop.abort();
  await new Promise((tick) => setTimeout(tick, 20));
  assert.equal(
    client.calls.includes("disconnect"),
    false,
    "disconnect waits for the message in progress",
  );
  release();

  assert.equal(await delivered, "committed");
  await running;
  assert.equal(client.calls.at(-1), "disconnect");
});

test("the shutdown deadline aborts retries of the message in progress", async () => {
  const client = new FakeConsumer();
  let signal: AbortSignal | undefined;
  const worker = new KafkaConsumerWorker({
    client,
    topics: ["t"],
    handler: {
      process: (_message, abort) => {
        signal = abort;
        return new Promise((_resolve, reject) => {
          abort.addEventListener("abort", () => reject(new Error("aborted")));
        });
      },
    },
  });
  await worker.start();
  const delivered = client.deliver(payload({}));

  await worker.shutdown(AbortSignal.timeout(10));

  assert.equal(signal?.aborted, true);
  assert.equal(await delivered, "redelivered");
});
