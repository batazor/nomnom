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
  ): Promise<KafkaPublisher> {
    const client = new kafka.KafkaJS.Kafka({
      kafkaJS: {
        brokers: [...config.brokers],
        clientId: config.clientId,
        logLevel: kafka.KafkaJS.logLevel.WARN,
      },
    });
    const producer = client.producer({
      kafkaJS: { idempotent: true, acks: -1, allowAutoTopicCreation: false },
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
