import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";
import kafka from "@confluentinc/kafka-javascript";
import {
  type DeadLetter,
  KafkaDeadLetters,
  REDRIVEN_FROM_HEADER,
} from "../pkg/infrastructure/kafka/dead-letters.ts";
import { KafkaPublisher } from "../pkg/infrastructure/kafka/publisher.ts";
import {
  DEAD_LETTER_HEADERS,
  deadLetterTopic,
} from "../pkg/infrastructure/messaging/processor.ts";
import { kafkaBrokers } from "./support/global-containers.ts";

// A group resolves only its own dead letters against the Kafka testcontainer
// of the global setup: discard and redrive move its <group>.dlq offset, the
// redriven record is the original event without the dead letter metadata,
// and the dead letters of another group stay.
describe("DLQ recovery with Kafka", () => {
  const run = randomUUID().slice(0, 8);
  // A stream and groups per run keep other runs' records and offsets out.
  const stream = `orders.v1.order-events.dlq-it-${run}`;
  const group = `restaurants-${run}`;
  const foreignGroup = `orders-${run}`;
  const kafkaConfig = {
    enabled: true as const,
    brokers: kafkaBrokers(),
    clientId: "restaurants-admin-test",
  };
  const client = new kafka.KafkaJS.Kafka({
    kafkaJS: {
      brokers: kafkaConfig.brokers,
      logLevel: kafka.KafkaJS.logLevel.ERROR,
    },
  });
  let publisher: KafkaPublisher;
  const opened: KafkaDeadLetters[] = [];

  const deadLetters = async (forGroup: string) => {
    const letters = await KafkaDeadLetters.connect(
      kafkaConfig,
      forGroup,
      publisher,
    );
    opened.push(letters);
    return letters;
  };

  // A record as MessageProcessor dead-letters it: envelope headers of the
  // event and the dead letter metadata of the group.
  const deadLetter = (forGroup: string, eventId: string, reason: string) =>
    publisher.publish({
      topic: deadLetterTopic(stream),
      key: "order-1",
      value: Buffer.from("payload"),
      headers: {
        event_id: eventId,
        event_type: "nomnom.orders.v1.OrderPlaced",
        aggregate_id: "order-1",
        occurred_at: "2026-10-26T12:00:00Z",
        [DEAD_LETTER_HEADERS.reason]: reason,
        [DEAD_LETTER_HEADERS.topic]: stream,
        [DEAD_LETTER_HEADERS.group]: forGroup,
        _watermill_message_uuid: eventId,
      },
    });

  before(async () => {
    const admin = client.admin();
    await admin.connect();
    await admin.createTopics({
      topics: [stream, deadLetterTopic(stream)].map((topic) => ({
        topic,
        numPartitions: 1,
        replicationFactor: 1,
      })),
    });
    await admin.disconnect();
    publisher = await KafkaPublisher.connect(kafkaConfig);
    await deadLetter(group, "event-discarded", "unknown event_type");
    await deadLetter(foreignGroup, "event-foreign", "permanent");
    await deadLetter(group, "event-redriven", "permanent");
  });

  after(async () => {
    await Promise.all(opened.map((letters) => letters.close()));
    await publisher.close();
  });

  test("lists, discards and redrives only the dead letters of the group", async () => {
    const letters = await deadLetters(group);
    const listed = await letters.list(stream);
    assert.deepEqual(eventIds(listed), ["event-discarded", "event-redriven"]);
    assert.equal(
      listed[0]?.headers[DEAD_LETTER_HEADERS.reason],
      "unknown event_type",
    );

    const seen: string[] = [];
    const discarded = await letters.discard(
      stream,
      { partition: 0, offset: listed[0]?.offset ?? -1 },
      (letter) => seen.push(letter.headers.event_id ?? ""),
    );
    assert.equal(discarded, 1);
    assert.deepEqual(seen, ["event-discarded"]);

    assert.equal(await letters.redrive(stream), 1);

    const record = await firstRecord(client, stream);
    assert.equal(record.key, "order-1");
    assert.equal(record.value, "payload");
    assert.equal(record.headers.event_id, "event-redriven");
    assert.equal(record.headers.event_type, "nomnom.orders.v1.OrderPlaced");
    assert.equal(
      record.headers[REDRIVEN_FROM_HEADER],
      `${deadLetterTopic(stream)}/0@2`,
    );
    for (const name of [
      ...Object.values(DEAD_LETTER_HEADERS),
      "_watermill_message_uuid",
    ]) {
      assert.equal(record.headers[name], undefined, name);
    }

    assert.deepEqual(
      await letters.list(stream),
      [],
      "resolved dead letters are not listed again",
    );
    assert.deepEqual(
      eventIds(await (await deadLetters(group)).list(stream)),
      [],
    );
    assert.deepEqual(
      eventIds(await (await deadLetters(foreignGroup)).list(stream)),
      ["event-foreign"],
    );
  });

  test("a DLQ nobody wrote to has no dead letters", async () => {
    const letters = await deadLetters(group);
    assert.deepEqual(await letters.list(`${stream}.missing`), []);
  });

  test("an operation bound to a missing partition fails", async () => {
    const letters = await deadLetters(group);
    await assert.rejects(
      letters.redrive(stream, { partition: 3, offset: 0 }),
      /has no partition 3/,
    );
  });
});

const eventIds = (letters: readonly DeadLetter[]): string[] =>
  letters.map((letter) => letter.headers.event_id ?? "");

type ReadRecord = Readonly<{
  key: string;
  value: string;
  headers: Readonly<Record<string, string>>;
}>;

/** The first record of `topic`, read by a consumer group of its own. */
async function firstRecord(
  client: InstanceType<typeof kafka.KafkaJS.Kafka>,
  topic: string,
): Promise<ReadRecord> {
  const consumer = client.consumer({
    kafkaJS: { groupId: `dlq-it-reader-${randomUUID()}`, fromBeginning: true },
  });
  await consumer.connect();
  await consumer.subscribe({ topics: [topic] });
  try {
    return await new Promise<ReadRecord>((resolve, reject) => {
      const timer = setTimeout(
        () => reject(new Error(`no redriven record on ${topic}`)),
        30_000,
      );
      void consumer.run({
        eachMessage: async ({ message }) => {
          clearTimeout(timer);
          const headers: Record<string, string> = {};
          for (const [name, value] of Object.entries(message.headers ?? {})) {
            const first = Array.isArray(value) ? value[0] : value;
            if (first !== undefined) headers[name] = first.toString();
          }
          resolve({
            key: message.key?.toString() ?? "",
            value: message.value?.toString() ?? "",
            headers,
          });
        },
      });
    });
  } finally {
    await consumer.disconnect();
  }
}
