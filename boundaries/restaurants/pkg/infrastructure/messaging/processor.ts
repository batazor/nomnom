// Обработка одного входящего Integration Event (ADR 11, ADR 28): envelope →
// handler по event_type внутри транзакции Inbox → классификация ошибки.
//
//   deserialization, permanent → <stream>.dlq + ack
//   transient                  → повтор с backoff; исчерпан → ошибка, Kafka доставит снова
//   business                   → ack, log
//   успех, включая duplicate Inbox → ack
//
// Неизвестный event_type подтверждается без обработки: стрим публикует и типы,
// которые этот consumer не потребляет.
import { setTimeout as delay } from "node:timers/promises";

import type { MessagePublisher } from "../outbox/relay.ts";
import {
  type Envelope,
  type InboundMessage,
  PermanentMessageError,
  parseEnvelope,
} from "./envelope.ts";

export type ErrorClass =
  | "transient"
  | "permanent"
  | "deserialization"
  | "business";

/** Обработчик одного типа Integration Event: payload → Command слайса. */
export interface EventHandler {
  /** Полное имя Protobuf-сообщения, например `nomnom.orders.v1.OrderPlaced`. */
  readonly eventType: string;
  /**
   * Выполняется внутри транзакции Inbox и может быть выполнен повторно: без
   * внешних побочных эффектов.
   */
  handle(envelope: Envelope, payload: Uint8Array): Promise<void>;
  /** Класс ошибки handler; `undefined` — transient. */
  classify(error: unknown): ErrorClass | undefined;
}

export type InboxRecord = Readonly<{
  /** Стабильное логическое имя обработчика, не экземпляр процесса. */
  consumer: string;
  eventId: string;
  eventType: string;
  aggregateId: string;
  topic: string;
}>;

/**
 * Transactional Inbox: `work` и запись `(consumer, event_id)` фиксируются
 * атомарно; уже обработанное событие — `duplicate` без вызова `work`.
 */
export interface Inbox {
  runOnce(
    record: InboxRecord,
    work: () => Promise<void>,
  ): Promise<"processed" | "duplicate">;
}

export type RetryPolicy = Readonly<{
  maxRetries: number;
  initialIntervalMs: number;
  maxIntervalMs: number;
}>;

export type ProcessOutcome =
  | "processed"
  | "duplicate"
  | "ignored"
  | "rejected"
  | "dead-lettered";

export type MessageLabels = Readonly<{
  handlerName: string;
  eventType: string;
  stream: string;
}>;

/** Метрики docs/observability/metric-inventory.md#kafka-собственный-otel-middleware-и-consumer-lag. */
export interface ConsumerMetrics {
  processed(
    labels: MessageLabels,
    durationS: number,
    errorClass?: string,
  ): void;
  retried(labels: MessageLabels): void;
  poisoned(labels: MessageLabels, errorClass: string): void;
  duplicate(labels: MessageLabels): void;
  messageAge(labels: MessageLabels, ageS: number): void;
}

export const noConsumerMetrics: ConsumerMetrics = {
  processed: () => {},
  retried: () => {},
  poisoned: () => {},
  duplicate: () => {},
  messageAge: () => {},
};

/** Dead letter topic стрима; общий для всех consumers этого стрима. */
export const deadLetterTopic = (topic: string): string => `${topic}.dlq`;

/** Metadata dead letter, совместимая с PoisonQueue Watermill в orders. */
export const DEAD_LETTER_HEADERS = Object.freeze({
  reason: "reason_poisoned",
  topic: "topic_poisoned",
  group: "consumer_group_poisoned",
});

export class MessageProcessor {
  readonly #consumer: string;
  readonly #group: string;
  readonly #handlers: ReadonlyMap<string, EventHandler>;
  readonly #inbox: Inbox;
  readonly #deadLetters: MessagePublisher;
  readonly #retry: RetryPolicy;
  readonly #metrics: ConsumerMetrics;
  readonly #log: (message: string) => void;
  readonly #warn: (message: string) => void;
  readonly #now: () => Date;

  constructor(options: {
    /** Имя обработчика в Inbox и label `handler_name`. */
    consumer: string;
    /** Consumer group Kafka, записывается в dead letters. */
    group: string;
    handlers: readonly EventHandler[];
    inbox: Inbox;
    deadLetters: MessagePublisher;
    retry: RetryPolicy;
    metrics?: ConsumerMetrics;
    log?: (message: string) => void;
    /** Dead letters and business rejections; `log` by default. */
    warn?: (message: string) => void;
    now?: () => Date;
  }) {
    this.#consumer = options.consumer;
    this.#group = options.group;
    this.#handlers = new Map(
      options.handlers.map((handler) => [handler.eventType, handler]),
    );
    this.#inbox = options.inbox;
    this.#deadLetters = options.deadLetters;
    this.#retry = options.retry;
    this.#metrics = options.metrics ?? noConsumerMetrics;
    this.#log = options.log ?? (() => {});
    this.#warn = options.warn ?? this.#log;
    this.#now = options.now ?? (() => new Date());
  }

  /**
   * Resolve — offset можно подтвердить; reject — transient-ошибка после всех
   * повторов или отмена `signal`: сообщение должно быть доставлено снова.
   */
  async process(
    message: InboundMessage,
    signal?: AbortSignal,
  ): Promise<ProcessOutcome> {
    const started = performance.now();
    const elapsed = () => (performance.now() - started) / 1000;
    let envelope: Envelope;
    try {
      envelope = parseEnvelope(message.headers);
    } catch (error) {
      const labels = this.#labels(message, message.headers.event_type ?? "");
      await this.#deadLetter(message, labels, error, "deserialization");
      this.#metrics.processed(labels, elapsed(), "deserialization");
      return "dead-lettered";
    }
    const labels = this.#labels(message, envelope.eventType);
    const handler = this.#handlers.get(envelope.eventType);
    if (handler === undefined) return "ignored";
    this.#metrics.messageAge(
      labels,
      Math.max(0, this.#now().getTime() - envelope.occurredAt.getTime()) / 1000,
    );

    const record: InboxRecord = {
      consumer: this.#consumer,
      eventId: envelope.eventId,
      eventType: envelope.eventType,
      aggregateId: envelope.aggregateId,
      topic: message.topic,
    };
    for (let attempt = 0; ; attempt += 1) {
      try {
        const result = await this.#inbox.runOnce(record, () =>
          handler.handle(envelope, message.value),
        );
        if (result === "duplicate") this.#metrics.duplicate(labels);
        this.#metrics.processed(labels, elapsed());
        return result;
      } catch (error) {
        const errorClass = classify(handler, error);
        if (errorClass === "permanent" || errorClass === "deserialization") {
          await this.#deadLetter(message, labels, error, errorClass);
          this.#metrics.processed(labels, elapsed(), errorClass);
          return "dead-lettered";
        }
        if (errorClass === "business") {
          this.#warn(
            `${this.#consumer} rejected ${envelope.eventType} event_id=${envelope.eventId}: ${describe(error)}`,
          );
          this.#metrics.processed(labels, elapsed(), "business");
          return "rejected";
        }
        if (attempt >= this.#retry.maxRetries || signal?.aborted) {
          this.#metrics.processed(labels, elapsed(), "transient");
          throw error;
        }
        this.#metrics.retried(labels);
        this.#log(
          `${this.#consumer} retrying ${envelope.eventType} event_id=${envelope.eventId} after transient error: ${describe(error)}`,
        );
        await delay(this.#backoff(attempt), undefined, { signal });
      }
    }
  }

  #backoff(attempt: number): number {
    return Math.min(
      this.#retry.initialIntervalMs * 2 ** attempt,
      this.#retry.maxIntervalMs,
    );
  }

  #labels(message: InboundMessage, eventType: string): MessageLabels {
    return { handlerName: this.#consumer, eventType, stream: message.topic };
  }

  async #deadLetter(
    message: InboundMessage,
    labels: MessageLabels,
    error: unknown,
    errorClass: string,
  ): Promise<void> {
    await this.#deadLetters.publish({
      topic: deadLetterTopic(message.topic),
      key: message.key === undefined ? "" : Buffer.from(message.key).toString(),
      value: message.value,
      headers: {
        ...message.headers,
        [DEAD_LETTER_HEADERS.reason]: describe(error),
        [DEAD_LETTER_HEADERS.topic]: message.topic,
        [DEAD_LETTER_HEADERS.group]: this.#group,
      },
    });
    this.#metrics.poisoned(labels, errorClass);
    this.#warn(
      `${this.#consumer} dead-lettered ${labels.eventType || "message"} ${message.topic}/${message.partition}@${message.offset} (${errorClass}): ${describe(error)}`,
    );
  }
}

const classify = (handler: EventHandler, error: unknown): ErrorClass => {
  if (error instanceof PermanentMessageError) return error.errorClass;
  return handler.classify(error) ?? "transient";
};

const describe = (error: unknown): string =>
  error instanceof Error ? error.message : String(error);
