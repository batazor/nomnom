import { setTimeout as delay } from "node:timers/promises";
import { promisify } from "node:util";

import kafka from "@confluentinc/kafka-javascript";

import type { KafkaConfig } from "../../config/config.ts";
import {
  DEAD_LETTER_HEADERS,
  deadLetterTopic,
} from "../messaging/processor.ts";
import type { KafkaPublisher } from "./publisher.ts";

/**
 * Header of a redriven message: the DLQ record it came from,
 * `<stream>.dlq/<partition>@<offset>`. A message that fails again carries it
 * into its new dead letter.
 */
export const REDRIVEN_FROM_HEADER = "redriven_from";

/**
 * Dead letter metadata a redrive does not publish: ours, the
 * `deserialization_error` of the Watermill consumers and the message UUID
 * their DLQ publisher sets (ADR 28, the same contract as `orders-admin`).
 */
const DEAD_LETTER_METADATA: ReadonlySet<string> = new Set([
  DEAD_LETTER_HEADERS.reason,
  DEAD_LETTER_HEADERS.topic,
  DEAD_LETTER_HEADERS.group,
  "deserialization_error",
  REDRIVEN_FROM_HEADER,
  "_watermill_message_uuid",
]);

/**
 * Suffix of the consumer group that tracks the resolved dead letters of a
 * group: `<group>.dlq`. Its committed offset on `<stream>.dlq` is the first
 * dead letter not yet redriven or discarded, so its lag is the unresolved DLQ
 * backlog of the group.
 */
export const RESOLVED_GROUP_SUFFIX = ".dlq";

/** Deadline of one metadata, watermark or offset request to the brokers. */
const REQUEST_TIMEOUT_MS = 10_000;

/**
 * Bound of the calls to the coordinator of `<group>.dlq` while it moves or
 * loads the offsets of the group, as after the first use of the group.
 */
const COORDINATOR_ATTEMPTS = 10;

/** Kafka errors of a coordinator that is not, or not yet, one. */
const COORDINATOR_NOT_READY: ReadonlySet<number> = new Set([
  kafka.CODES.ERRORS.ERR_COORDINATOR_LOAD_IN_PROGRESS,
  kafka.CODES.ERRORS.ERR_COORDINATOR_NOT_AVAILABLE,
  kafka.CODES.ERRORS.ERR_NOT_COORDINATOR,
]);

/** One record of `<stream>.dlq`. */
export type DeadLetter = Readonly<{
  partition: number;
  offset: number;
  /** Time of the DLQ record. */
  deadLetteredAt: Date;
  key: Buffer;
  value: Buffer;
  /** Envelope of the event and the dead letter metadata. */
  headers: Readonly<Record<string, string>>;
}>;

/** One partition of the DLQ up to an offset inclusive. */
export type Through = Readonly<{ partition: number; offset: number }>;

type Consumer = InstanceType<typeof kafka.KafkaConsumer>;
type KafkaRecord = kafka.Message;

/**
 * Lists, redrives and discards the dead letters of one consumer group
 * (ADR 28, runbook docs/dlq.md). Dead letters of other groups sharing the DLQ
 * are passed over: each group resolves its own. The DLQ is read from the
 * committed offset of `<group>.dlq` up to the end the DLQ had when the
 * operation began. The group has no members: its offsets are committed on a
 * manually assigned partition, outside any generation.
 */
export class KafkaDeadLetters {
  readonly #consumer: Consumer;
  readonly #publisher: KafkaPublisher;
  readonly #group: string;

  private constructor(
    consumer: Consumer,
    publisher: KafkaPublisher,
    group: string,
  ) {
    this.#consumer = consumer;
    this.#publisher = publisher;
    this.#group = group;
  }

  /** `publisher` redrives the dead letters; the caller closes it. */
  static async connect(
    config: KafkaConfig,
    group: string,
    publisher: KafkaPublisher,
  ): Promise<KafkaDeadLetters> {
    const consumer = new kafka.KafkaConsumer(
      {
        "bootstrap.servers": config.brokers.join(","),
        "client.id": config.clientId,
        "group.id": `${group}${RESOLVED_GROUP_SUFFIX}`,
        "enable.auto.commit": false,
        "enable.auto.offset.store": false,
        "allow.auto.create.topics": false,
      },
      // Resolved offsets behind the retention of the DLQ start at its oldest
      // record.
      { "auto.offset.reset": "earliest" },
    );
    await promisify<{ timeout: number }, unknown>(
      consumer.connect.bind(consumer),
    )({ timeout: REQUEST_TIMEOUT_MS });
    return new KafkaDeadLetters(consumer, publisher, group);
  }

  async close(): Promise<void> {
    await promisify(this.#consumer.disconnect.bind(this.#consumer))();
  }

  /**
   * Unresolved dead letters of the group on the DLQ of `stream`. A DLQ that
   * does not exist yet has none.
   */
  async list(stream: string, signal?: AbortSignal): Promise<DeadLetter[]> {
    const letters: DeadLetter[] = [];
    await this.#scan(stream, undefined, false, signal, async (letter) => {
      letters.push(letter);
    });
    return letters;
  }

  /**
   * Publishes the unresolved dead letters of the group back to `stream` with
   * their key, payload and envelope headers, without the dead letter
   * metadata, and marks each resolved after the broker accepted it. Every
   * consumer group of the stream receives the event again; the Inbox makes it
   * a no-op for groups that already processed it. Without `through` every
   * partition is redriven.
   */
  async redrive(
    stream: string,
    through?: Through,
    signal?: AbortSignal,
  ): Promise<number> {
    let redriven = 0;
    await this.#scan(stream, through, true, signal, async (letter) => {
      const origin = letter.headers[DEAD_LETTER_HEADERS.topic];
      if (origin !== stream) {
        throw new Error(
          `dead letter ${letter.partition}@${letter.offset} came from topic "${origin}", not ${stream}`,
        );
      }
      const headers: Record<string, string> = {};
      for (const [name, value] of Object.entries(letter.headers)) {
        if (!DEAD_LETTER_METADATA.has(name)) headers[name] = value;
      }
      headers[REDRIVEN_FROM_HEADER] =
        `${deadLetterTopic(stream)}/${letter.partition}@${letter.offset}`;
      await this.#publisher.publish({
        topic: stream,
        key: letter.key.toString(),
        value: letter.value,
        headers,
      });
      redriven += 1;
    });
    return redriven;
  }

  /**
   * Marks the unresolved dead letters of the group on one partition up to
   * `through` resolved without publishing them; `seen` receives each one
   * first, so the caller records what was discarded.
   */
  async discard(
    stream: string,
    through: Through,
    seen: (letter: DeadLetter) => void,
    signal?: AbortSignal,
  ): Promise<number> {
    let discarded = 0;
    await this.#scan(stream, through, true, signal, async (letter) => {
      seen(letter);
      discarded += 1;
    });
    return discarded;
  }

  /**
   * Passes the unresolved dead letters of the group to `fn` in partition
   * order. With `resolve`, the offset of `<group>.dlq` moves past every record
   * `fn` accepted, records of other groups included, and is committed record
   * by record: an interrupted operation resumes after the last resolved one.
   */
  async #scan(
    stream: string,
    through: Through | undefined,
    resolve: boolean,
    signal: AbortSignal | undefined,
    fn: (letter: DeadLetter) => Promise<void>,
  ): Promise<void> {
    const topic = deadLetterTopic(stream);
    const partitions = await this.#partitions(topic);
    if (partitions === undefined) return;
    if (through !== undefined && !partitions.includes(through.partition)) {
      throw new Error(`${topic} has no partition ${through.partition}`);
    }
    for (const partition of partitions) {
      if (through !== undefined && partition !== through.partition) continue;
      await this.#scanPartition(topic, partition, through, resolve, signal, fn);
    }
  }

  async #scanPartition(
    topic: string,
    partition: number,
    through: Through | undefined,
    resolve: boolean,
    signal: AbortSignal | undefined,
    fn: (letter: DeadLetter) => Promise<void>,
  ): Promise<void> {
    const watermarks = await promisify<
      string,
      number,
      number,
      kafka.WatermarkOffsets
    >(this.#consumer.queryWatermarkOffsets.bind(this.#consumer))(
      topic,
      partition,
      REQUEST_TIMEOUT_MS,
    );
    let end = watermarks.highOffset;
    if (through !== undefined && through.offset + 1 < end) {
      end = through.offset + 1;
    }
    const resolved = await this.#resolvedOffset(topic, partition);
    const next = Math.max(resolved, watermarks.lowOffset);
    if (next >= end) return;

    this.#consumer.assign([{ topic, partition, offset: next }]);
    try {
      for (;;) {
        signal?.throwIfAborted();
        const records = await promisify<number, KafkaRecord[]>(
          this.#consumer.consume.bind(this.#consumer),
        )(100);
        // Offsets of control records and compacted records have no record:
        // the scan ends when the position reaches the end.
        if (records.length === 0 && this.#position(topic, partition) >= end) {
          return;
        }
        for (const record of records) {
          const letter = deadLetter(record);
          if (letter.headers[DEAD_LETTER_HEADERS.group] === this.#group) {
            await fn(letter);
          }
          if (resolve) {
            await withCoordinator(() =>
              this.#consumer.commitSync({
                topic,
                partition,
                offset: record.offset + 1,
              }),
            );
          }
          if (record.offset + 1 >= end) return;
        }
      }
    } finally {
      this.#consumer.unassign();
    }
  }

  /** Next offset the consumer fetches; -1 — none fetched yet. */
  #position(topic: string, partition: number): number {
    const [position] = this.#consumer.position([{ topic, partition }]);
    return position === undefined || position.offset < 0 ? -1 : position.offset;
  }

  /** Partitions of `topic`; `undefined` — the topic does not exist. */
  async #partitions(topic: string): Promise<number[] | undefined> {
    const metadata = await promisify<{ timeout: number }, kafka.Metadata>(
      this.#consumer.getMetadata.bind(this.#consumer),
    )({
      timeout: REQUEST_TIMEOUT_MS,
    });
    const found = metadata.topics.find(({ name }) => name === topic);
    return found?.partitions.map(({ id }) => id).sort((a, b) => a - b);
  }

  /** Committed offset of `<group>.dlq`; -1 — nothing resolved yet. */
  async #resolvedOffset(topic: string, partition: number): Promise<number> {
    const committed = await withCoordinator(async () => {
      const [offset] = await promisify<
        kafka.TopicPartition[],
        number,
        kafka.TopicPartitionOffsetAndMetadata[]
      >(this.#consumer.committed.bind(this.#consumer))(
        [{ topic, partition }],
        REQUEST_TIMEOUT_MS,
      );
      if (offset?.error) throw offset.error;
      return offset;
    });
    return committed === undefined || committed.offset < 0
      ? -1
      : committed.offset;
  }
}

/**
 * Calls `fn` again while the coordinator of `<group>.dlq` answers that it is
 * not, or not yet, one; librdkafka finds the coordinator again meanwhile.
 */
async function withCoordinator<T>(fn: () => T | Promise<T>): Promise<T> {
  for (let attempt = 1; ; attempt += 1) {
    try {
      return await fn();
    } catch (error) {
      const code = (error as { code?: unknown }).code;
      if (
        attempt >= COORDINATOR_ATTEMPTS ||
        typeof code !== "number" ||
        !COORDINATOR_NOT_READY.has(code)
      ) {
        throw error;
      }
      await delay(attempt * 100);
    }
  }
}

function deadLetter(record: KafkaRecord): DeadLetter {
  const headers: Record<string, string> = {};
  for (const header of record.headers ?? []) {
    for (const [name, value] of Object.entries(header)) {
      headers[name] = typeof value === "string" ? value : value.toString();
    }
  }
  if (record.timestamp === undefined) {
    throw new Error(
      `${record.topic}/${record.partition}@${record.offset} has no timestamp`,
    );
  }
  return {
    partition: record.partition,
    offset: record.offset,
    deadLetteredAt: new Date(record.timestamp),
    key: toBuffer(record.key),
    value: record.value ?? Buffer.alloc(0),
    headers,
  };
}

const toBuffer = (key: kafka.MessageKey): Buffer =>
  typeof key === "string" ? Buffer.from(key) : (key ?? Buffer.alloc(0));
