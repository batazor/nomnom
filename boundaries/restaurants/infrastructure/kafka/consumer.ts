import kafka from "@confluentinc/kafka-javascript";
import { type Counter, metrics } from "@opentelemetry/api";

import type { Worker } from "../../pkg/lifecycle/lifecycle.ts";
import { extractContext, SpanKind, withSpan } from "../../pkg/telemetry/tracing.ts";
import type { InboundMessage } from "../messaging/envelope.ts";
import type { ProcessOutcome } from "../messaging/processor.ts";

/** Kafka-клиент consumer'а в объёме, нужном worker (KafkaJS-совместимый API). */
export interface ConsumerClient {
  connect(): Promise<void>;
  subscribe(subscription: { topics: string[] }): Promise<void>;
  run(config: {
    eachMessage: (payload: EachMessage) => Promise<void>;
  }): Promise<void>;
  disconnect(): Promise<void>;
}

export type EachMessage = Readonly<{
  topic: string;
  partition: number;
  message: Readonly<{
    key: Buffer | null;
    value: Buffer | null;
    offset: string;
    headers?: Readonly<
      Record<string, Buffer | string | (Buffer | string)[] | undefined>
    >;
  }>;
}>;

export const KAFKA_INSTRUMENTATION = "nomnom.restaurants.kafka";

export interface MessageHandler {
  process(
    message: InboundMessage,
    signal: AbortSignal,
  ): Promise<ProcessOutcome>;
}

export type KafkaConsumerConfig = Readonly<{
  brokers: readonly string[];
  clientId: string;
  groupId: string;
  /** `true` — новая группа читает стрим с начала (`oldest`). */
  fromBeginning: boolean;
}>;

/**
 * KafkaJS-совместимый consumer `@confluentinc/kafka-javascript` (ADR 31) без
 * подключения: его открывает {@link KafkaConsumerWorker.start}.
 */
export function createConsumerClient(
  config: KafkaConsumerConfig,
): ConsumerClient {
  const client = new kafka.KafkaJS.Kafka({
    kafkaJS: {
      brokers: [...config.brokers],
      clientId: config.clientId,
      logLevel: kafka.KafkaJS.logLevel.WARN,
    },
  });
  return client.consumer({
    kafkaJS: {
      groupId: config.groupId,
      fromBeginning: config.fromBeginning,
      autoCommit: true,
      // Как orders-consumer: topic стрима создаётся при первом обращении, если
      // это разрешает политика брокера (в kind разрешено).
      allowAutoTopicCreation: true,
    },
  }) as unknown as ConsumerClient;
}

/**
 * Consumer group как {@link Worker} runtime contract. Offset сообщения
 * сохраняется только после успешного `process` и коммитится автоматически;
 * ошибка `process` возвращает consumer к этому offset (KafkaJS-семантика
 * `@confluentinc/kafka-javascript`), сообщение доставляется снова. По
 * `shutdown` начатая обработка завершается до deadline, после чего её повторы
 * прерываются; `disconnect` коммитит сохранённые offsets.
 *
 * Semconv `messaging.client.consumed.messages` (v1.44.0, inventory section
 * messaging-otel): one per message delivered to `process`, after it settles;
 * a rejected `process` (redelivery) carries `error.type` — the error name.
 * The counter lives on the global MeterProvider and is no-op until telemetry
 * starts.
 */
export class KafkaConsumerWorker implements Worker {
  readonly #client: ConsumerClient;
  readonly #topics: readonly string[];
  readonly #handler: MessageHandler;
  readonly #abort = new AbortController();
  readonly #stopped: Promise<void>;
  #resolveStopped: () => void = () => {};

  readonly #groupId: string | undefined;
  readonly #consumed: Counter;

  constructor(options: {
    client: ConsumerClient;
    topics: readonly string[];
    handler: MessageHandler;
    /** `messaging.consumer.group.name` of the process spans. */
    groupId?: string;
  }) {
    this.#client = options.client;
    this.#groupId = options.groupId;
    this.#topics = options.topics;
    this.#handler = options.handler;
    this.#stopped = new Promise((resolve) => {
      this.#resolveStopped = resolve;
    });
    this.#consumed = metrics
      .getMeter(KAFKA_INSTRUMENTATION)
      .createCounter("messaging.client.consumed.messages", {
        description:
          "Number of messages that were delivered to the application.",
        unit: "{message}",
      });
  }

  async start(): Promise<void> {
    await this.#client.connect();
    await this.#client.subscribe({ topics: [...this.#topics] });
    await this.#client.run({
      eachMessage: async ({ topic, partition, message }) => {
        const headers = stringHeaders(message.headers);
        // Process span of one message: the child of the producer's
        // `traceparent` header (one message, one immediate processing —
        // docs/observability.md, "Kafka и Watermill"), so the Inbox write and
        // the Outbox records of the handler stay in the same trace. A
        // redelivery is a separate process span with the same parent.
        await withSpan(
          `process ${topic}`,
          {
            parent: extractContext(headers),
            kind: SpanKind.CONSUMER,
            attributes: {
              "messaging.system": "kafka",
              "messaging.operation.type": "process",
              "messaging.operation.name": "process",
              "messaging.destination.name": topic,
              "messaging.destination.partition.id": String(partition),
              "messaging.kafka.offset": Number(message.offset),
              ...(this.#groupId === undefined
                ? {}
                : { "messaging.consumer.group.name": this.#groupId }),
              ...(headers.event_id === undefined
                ? {}
                : { "messaging.message.id": headers.event_id }),
            },
          },
          () =>
            this.#consumedMessage(
              topic,
              this.#handler.process(
                {
                  topic,
                  partition,
                  offset: message.offset,
                  key: message.key ?? undefined,
                  value: message.value ?? new Uint8Array(),
                  headers,
                },
                this.#abort.signal,
              ),
            ),
        );
      },
    });
  }

  run(): Promise<void> {
    return this.#stopped;
  }

  async #consumedMessage(
    topic: string,
    processing: Promise<ProcessOutcome>,
  ): Promise<ProcessOutcome> {
    const attributes = {
      "messaging.system": "kafka",
      "messaging.destination.name": topic,
      ...(this.#groupId === undefined
        ? {}
        : { "messaging.consumer.group.name": this.#groupId }),
    };
    try {
      const outcome = await processing;
      this.#consumed.add(1, attributes);
      return outcome;
    } catch (error) {
      this.#consumed.add(1, {
        ...attributes,
        "error.type": error instanceof Error ? error.name : "_OTHER",
      });
      throw error;
    }
  }

  async shutdown(deadline: AbortSignal): Promise<void> {
    const abort = () => this.#abort.abort();
    if (deadline.aborted) abort();
    deadline.addEventListener("abort", abort, { once: true });
    try {
      await this.#client.disconnect();
    } finally {
      deadline.removeEventListener("abort", abort);
      this.#resolveStopped();
    }
  }
}

/** Headers Kafka → строки; повтор заголовка — первое значение. */
export function stringHeaders(
  headers: EachMessage["message"]["headers"],
): Record<string, string> {
  const result: Record<string, string> = {};
  for (const [name, raw] of Object.entries(headers ?? {})) {
    const value = Array.isArray(raw) ? raw[0] : raw;
    if (value === undefined) continue;
    result[name] = typeof value === "string" ? value : value.toString("utf8");
  }
  return result;
}
