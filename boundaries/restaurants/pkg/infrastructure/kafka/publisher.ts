import kafka from "@confluentinc/kafka-javascript";
import { context } from "@opentelemetry/api";

import type { KafkaConfig } from "../../config/config.ts";
import { injectContext, SpanKind, withSpan } from "../../telemetry/tracing.ts";
import type {
  IntegrationMessage,
  MessagePublisher,
} from "../messaging/envelope.ts";

type Producer = ReturnType<
  InstanceType<typeof kafka.KafkaJS.Kafka>["producer"]
>;

/**
 * Idempotent Kafka producer with `acks=all` of the consumer dead letters
 * (ADR 28): `publish` resolves after the broker acknowledged the message, so
 * the offset is committed only for a stored dead letter. Each publish is a
 * PRODUCER span `send <topic>` that continues the active context; its W3C
 * `traceparent`/`tracestate` replace those of `message.headers`.
 */
export class KafkaPublisher implements MessagePublisher {
  readonly #producer: Producer;

  private constructor(producer: Producer) {
    this.#producer = producer;
  }

  static async connect(
    config: Extract<KafkaConfig, { enabled: true }>,
    options: {
      /**
       * Разрешить брокеру создать topic при первой публикации, если это
       * допускает его политика: dead letters `<stream>.dlq` consumer'а.
       */
      allowAutoTopicCreation?: boolean;
    } = {},
  ): Promise<KafkaPublisher> {
    const client = new kafka.KafkaJS.Kafka({
      kafkaJS: {
        brokers: [...config.brokers],
        clientId: config.clientId,
        logLevel: kafka.KafkaJS.logLevel.WARN,
      },
    });
    const producer = client.producer({
      kafkaJS: {
        idempotent: true,
        acks: -1,
        allowAutoTopicCreation: options.allowAutoTopicCreation ?? false,
      },
    });
    await producer.connect();
    return new KafkaPublisher(producer);
  }

  async publish(message: IntegrationMessage): Promise<void> {
    await sendWithTraceContext(message, (headers) =>
      this.#producer.send({
        topic: message.topic,
        messages: [
          {
            key: message.key,
            value: Buffer.from(
              message.value.buffer,
              message.value.byteOffset,
              message.value.byteLength,
            ),
            headers,
          },
        ],
      }),
    );
  }

  async close(): Promise<void> {
    await this.#producer.disconnect();
  }
}

/**
 * PRODUCER span `send <topic>` around `send`, which receives the message
 * headers with the W3C context of that span injected.
 */
export const sendWithTraceContext = (
  message: IntegrationMessage,
  send: (headers: Record<string, string>) => Promise<unknown>,
): Promise<void> =>
  withSpan(
    `send ${message.topic}`,
    {
      parent: context.active(),
      kind: SpanKind.PRODUCER,
      attributes: producerAttributes(message),
    },
    async () => {
      const headers = { ...message.headers };
      injectContext(context.active(), headers);
      await send(headers);
    },
  );

/** Semconv messaging attributes of a publish; never the payload. */
export const producerAttributes = (
  message: IntegrationMessage,
): Record<string, string> => ({
  "messaging.system": "kafka",
  "messaging.operation.type": "send",
  "messaging.operation.name": "send",
  "messaging.destination.name": message.topic,
  "messaging.kafka.message.key": message.key,
  ...(message.headers.event_id === undefined
    ? {}
    : { "messaging.message.id": message.headers.event_id }),
});
