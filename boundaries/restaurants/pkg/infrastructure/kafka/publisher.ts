import kafka from "@confluentinc/kafka-javascript";

import type { KafkaConfig } from "../../config/config.ts";
import type { IntegrationMessage, MessagePublisher } from "../outbox/relay.ts";

type Producer = ReturnType<
  InstanceType<typeof kafka.KafkaJS.Kafka>["producer"]
>;

/**
 * Idempotent Kafka producer with `acks=all` (ADR 31): `publish` resolves after
 * the broker acknowledged the message, so the relay sets `publishedAt` only
 * for confirmed records.
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
      /**
       * `message.timeout.ms`: upper bound of one `publish` including retries.
       * The relay keeps it well below its lease TTL so a publish never
       * outlives the lease (librdkafka default is 300 s).
       */
      messageTimeoutMs?: number;
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
      ...(options.messageTimeoutMs === undefined
        ? {}
        : { "message.timeout.ms": options.messageTimeoutMs }),
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
    await this.#producer.send({
      topic: message.topic,
      messages: [
        {
          key: message.key,
          value: Buffer.from(
            message.value.buffer,
            message.value.byteOffset,
            message.value.byteLength,
          ),
          headers: { ...message.headers },
        },
      ],
    });
  }

  async close(): Promise<void> {
    await this.#producer.disconnect();
  }
}
