import { z } from "zod";

import {
  DEFAULT_KAFKA_CLIENT_ID,
  DEFAULT_KAFKA_CONSUMER_GROUP,
  DEFAULT_KAFKA_INITIAL_OFFSET,
  DEFAULT_SCHEMA_REGISTRY_TIMEOUT,
  DEFAULT_SPICEDB_TIMEOUT,
} from "./defaults.ts";
import type { Spec } from "./file.ts";
import { optionalStringSchema, positiveDurationSchema } from "./schemas.ts";

/** Secret: bearer preshared key SpiceDB, только из env (Kubernetes Secret). */
export const SPICEDB_PRESHARED_KEY_ENV = "SPICEDB__PRESHARED_KEY";

/** `spec.spicedb`: подключение к SpiceDB без preshared key из Secret. */
export const spicedbSectionSchema = z
  .strictObject({
    /** `host:port` gRPC API SpiceDB; обязателен процессу restaurants. */
    endpoint: optionalStringSchema,
    insecure: z.boolean().prefault(false),
    timeout: positiveDurationSchema.prefault(DEFAULT_SPICEDB_TIMEOUT),
  })
  .prefault({});

/** `spec.kafka`: брокеры, client id и consumer group restaurants-consumer. */
export const kafkaSectionSchema = z
  .strictObject({
    /** `host:port` через запятую; обязателен consumer и restaurants-admin. */
    brokers: optionalStringSchema,
    clientId: z.string().trim().min(1).prefault(DEFAULT_KAFKA_CLIENT_ID),
    consumerGroup: z
      .string()
      .trim()
      .regex(/^[A-Za-z0-9._-]{1,249}$/, "expected a Kafka consumer group")
      .prefault(DEFAULT_KAFKA_CONSUMER_GROUP),
    initialOffset: z
      .enum(["oldest", "newest"], { error: "expected oldest or newest" })
      .prefault(DEFAULT_KAFKA_INITIAL_OFFSET),
  })
  .prefault({});

/** `spec.schemaRegistry`: Schema Registry записей стримов (FR-715). */
export const schemaRegistrySectionSchema = z
  .strictObject({
    /** Обязателен consumer: без него запись не проверить. */
    url: optionalStringSchema,
    timeout: positiveDurationSchema.prefault(DEFAULT_SCHEMA_REGISTRY_TIMEOUT),
  })
  .prefault({});

/** Подключение к SpiceDB (ADR 18); `spec.spicedb.endpoint` обязателен. */
export type SpiceDbConfig = Readonly<{
  /** `host:port` gRPC API SpiceDB. */
  endpoint: string;
  /** Bearer preshared key; приходит из Secret, не из файла конфигурации. */
  presharedKey: string;
  /** Plaintext gRPC — только kind и локальный запуск. */
  insecure: boolean;
  /** Deadline одной проверки permission. */
  timeoutMs: number;
}>;

/**
 * Kafka брокеры consumer и admin (ADR 28); `spec.kafka.brokers` обязателен.
 * Основной процесс к Kafka не подключается (Outbox публикует Debezium, ADR 37).
 */
export type KafkaConfig = Readonly<{
  brokers: readonly string[];
  clientId: string;
}>;

export function loadSpiceDbConfig(
  spec: Pick<Spec, "spicedb">,
  environment: Readonly<Record<string, string | undefined>>,
): SpiceDbConfig {
  const endpoint = spec.spicedb.endpoint ?? "";
  if (endpoint === "") {
    throw new Error(
      "spec.spicedb.endpoint is required: restaurants checks permissions in SpiceDB",
    );
  }
  const presharedKey = environment[SPICEDB_PRESHARED_KEY_ENV]?.trim() ?? "";
  if (presharedKey === "") {
    throw new Error(
      `${SPICEDB_PRESHARED_KEY_ENV} must not be empty when spec.spicedb.endpoint is set`,
    );
  }
  return Object.freeze({
    endpoint,
    presharedKey,
    insecure: spec.spicedb.insecure,
    timeoutMs: spec.spicedb.timeout,
  });
}

const BROKER = /^[A-Za-z0-9._-]+:\d{1,5}$/;

export function loadKafkaConfig(spec: Pick<Spec, "kafka">): KafkaConfig {
  const raw = spec.kafka.brokers ?? "";
  if (raw === "") {
    throw new Error(
      "spec.kafka.brokers is required: Integration Events are read from Kafka",
    );
  }
  const brokers = raw.split(",").map((broker) => broker.trim());
  for (const broker of brokers) {
    if (!BROKER.test(broker)) {
      throw new Error(
        `spec.kafka.brokers: malformed broker "${broker}", expected host:port`,
      );
    }
  }
  return Object.freeze({
    brokers: Object.freeze(brokers),
    clientId: spec.kafka.clientId,
  });
}

/**
 * Schema Registry записей стримов (FR-715, FR-719); `spec.schemaRegistry.url`
 * обязателен consumer: без него запись не проверить.
 */
export type SchemaRegistryConfig = Readonly<{
  /** Базовый URL с завершающим `/`: пути API разрешаются относительно него. */
  url: URL;
  timeoutMs: number;
}>;

export function loadSchemaRegistryConfig(
  spec: Pick<Spec, "schemaRegistry">,
): SchemaRegistryConfig {
  const raw = spec.schemaRegistry.url ?? "";
  if (raw === "") {
    throw new Error(
      "spec.schemaRegistry.url is required: records of the streams reference schemas of the Schema Registry",
    );
  }
  let url: URL;
  try {
    url = new URL(raw.endsWith("/") ? raw : `${raw}/`);
  } catch {
    throw new Error(`spec.schemaRegistry.url: malformed url "${raw}"`);
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error(`spec.schemaRegistry.url: "${raw}" must be an http(s) url`);
  }
  return Object.freeze({ url, timeoutMs: spec.schemaRegistry.timeout });
}
