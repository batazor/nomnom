import {
  DEFAULT_KAFKA_CLIENT_ID,
  DEFAULT_SPICEDB_TIMEOUT,
  KAFKA_BROKERS_ENV,
  KAFKA_CLIENT_ID_ENV,
  SPICEDB_ENDPOINT_ENV,
  SPICEDB_INSECURE_ENV,
  SPICEDB_PRESHARED_KEY_ENV,
  SPICEDB_TIMEOUT_ENV,
} from "./env.ts";
import { booleanSchema, durationSchema } from "./schemas.ts";

/** Подключение к SpiceDB (ADR 18), когда задан `SPICEDB__ENDPOINT`. */
export type SpiceDbEnabledConfig = Readonly<{
  enabled: true;
  /** `host:port` gRPC API SpiceDB. */
  endpoint: string;
  /** Bearer preshared key; приходит из Secret, не из values. */
  presharedKey: string;
  /** Plaintext gRPC — только kind и локальный запуск. */
  insecure: boolean;
  /** Deadline одной проверки permission. */
  timeoutMs: number;
}>;

/**
 * `enabled: false` при пустом `SPICEDB__ENDPOINT`: `cmd/restaurants` тогда не
 * стартует (SpiceDB обязателен).
 */
export type SpiceDbConfig = Readonly<{ enabled: false }> | SpiceDbEnabledConfig;

/**
 * Kafka брокеры consumer (ADR 28). `enabled: false` при пустом
 * `KAFKA__BROKERS`; restaurants-consumer в этом случае не стартует, основной
 * процесс к Kafka не подключается (Outbox публикует Debezium, ADR 37).
 */
export type KafkaConfig =
  | Readonly<{ enabled: false }>
  | Readonly<{ enabled: true; brokers: readonly string[]; clientId: string }>;

export function loadSpiceDbConfig(
  environment: Record<string, string | undefined>,
): SpiceDbConfig {
  const endpoint = environment[SPICEDB_ENDPOINT_ENV]?.trim() ?? "";
  if (endpoint === "") {
    return Object.freeze({ enabled: false });
  }
  const presharedKey = environment[SPICEDB_PRESHARED_KEY_ENV]?.trim() ?? "";
  if (presharedKey === "") {
    throw new Error(
      `${SPICEDB_PRESHARED_KEY_ENV} must not be empty when ${SPICEDB_ENDPOINT_ENV} is set`,
    );
  }
  const insecure = booleanSchema.parse(
    environment[SPICEDB_INSECURE_ENV]?.trim() || "false",
  );
  const timeoutMs = durationSchema
    .refine((value) => value > 0, "SpiceDB timeout must be positive")
    .parse(environment[SPICEDB_TIMEOUT_ENV]?.trim() || DEFAULT_SPICEDB_TIMEOUT);
  return Object.freeze({
    enabled: true,
    endpoint,
    presharedKey,
    insecure,
    timeoutMs,
  });
}

const BROKER = /^[A-Za-z0-9._-]+:\d{1,5}$/;

export function loadKafkaConfig(
  environment: Record<string, string | undefined>,
): KafkaConfig {
  const raw = environment[KAFKA_BROKERS_ENV]?.trim() ?? "";
  if (raw === "") return Object.freeze({ enabled: false });
  const brokers = raw.split(",").map((broker) => broker.trim());
  for (const broker of brokers) {
    if (!BROKER.test(broker)) {
      throw new Error(
        `${KAFKA_BROKERS_ENV}: malformed broker "${broker}", expected host:port`,
      );
    }
  }
  const clientId =
    environment[KAFKA_CLIENT_ID_ENV]?.trim() || DEFAULT_KAFKA_CLIENT_ID;
  return Object.freeze({
    enabled: true,
    brokers: Object.freeze(brokers),
    clientId,
  });
}
