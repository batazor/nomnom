import kafka from "@confluentinc/kafka-javascript";

import type { Worker } from "../../lifecycle/lifecycle.ts";
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
 */
export class KafkaConsumerWorker implements Worker {
  readonly #client: ConsumerClient;
  readonly #topics: readonly string[];
  readonly #handler: MessageHandler;
  readonly #abort = new AbortController();
  readonly #stopped: Promise<void>;
  #resolveStopped: () => void = () => {};

  constructor(options: {
    client: ConsumerClient;
    topics: readonly string[];
    handler: MessageHandler;
  }) {
    this.#client = options.client;
    this.#topics = options.topics;
    this.#handler = options.handler;
    this.#stopped = new Promise((resolve) => {
      this.#resolveStopped = resolve;
    });
  }

  async start(): Promise<void> {
    await this.#client.connect();
    await this.#client.subscribe({ topics: [...this.#topics] });
    await this.#client.run({
      eachMessage: async ({ topic, partition, message }) => {
        await this.#handler.process(
          {
            topic,
            partition,
            offset: message.offset,
            key: message.key ?? undefined,
            value: message.value ?? new Uint8Array(),
            headers: stringHeaders(message.headers),
          },
          this.#abort.signal,
        );
      },
    });
  }

  run(): Promise<void> {
    return this.#stopped;
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
