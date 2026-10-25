import {
  DEFAULT_KAFKA_CLIENT_ID,
  DEFAULT_MONGODB_DATABASE,
  DEFAULT_MONGODB_TIMEOUT,
  DEFAULT_SPICEDB_TIMEOUT,
  KAFKA_BROKERS_ENV,
  KAFKA_CLIENT_ID_ENV,
  MONGODB_DATABASE_ENV,
  MONGODB_TIMEOUT_ENV,
  MONGODB_URI_ENV,
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
 * `enabled: false` при пустом `SPICEDB__ENDPOINT`: авторизация остаётся на
 * in-memory адаптерах без grants (deny-all).
 */
export type SpiceDbConfig = Readonly<{ enabled: false }> | SpiceDbEnabledConfig;

/** Подключение к MongoDB (ADR 8, локальный ADR 1), когда задан `MONGODB__URI`. */
export type MongoDbEnabledConfig = Readonly<{
  enabled: true;
  /** Connection string replica set; приходит из Secret, не из values. */
  uri: string;
  database: string;
  /** Server selection timeout: недоступная MongoDB останавливает старт. */
  timeoutMs: number;
}>;

/**
 * `enabled: false` при пустом `MONGODB__URI`; процессы `restaurants` и
 * `restaurants-migrate` в этом случае не стартуют.
 */
export type MongoDbConfig = Readonly<{ enabled: false }> | MongoDbEnabledConfig;

/**
 * Kafka брокеры relay (ADR 31) и consumer (ADR 28). `enabled: false` при
 * пустом `KAFKA__BROKERS`; restaurants-relay и restaurants-consumer в этом
 * случае не стартуют, основной процесс к Kafka не подключается.
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

const MONGODB_DATABASE_NAME = /^[A-Za-z0-9_-]{1,63}$/;

export function loadMongoDbConfig(
  environment: Record<string, string | undefined>,
): MongoDbConfig {
  const uri = environment[MONGODB_URI_ENV]?.trim() ?? "";
  if (uri === "") {
    return Object.freeze({ enabled: false });
  }
  if (!/^mongodb(\+srv)?:\/\//.test(uri)) {
    throw new Error(
      `${MONGODB_URI_ENV} must be a mongodb:// or mongodb+srv:// connection string`,
    );
  }
  const database =
    environment[MONGODB_DATABASE_ENV]?.trim() || DEFAULT_MONGODB_DATABASE;
  if (!MONGODB_DATABASE_NAME.test(database)) {
    throw new Error(
      `${MONGODB_DATABASE_ENV}: invalid database name "${database}"`,
    );
  }
  const timeoutMs = durationSchema
    .refine((value) => value > 0, "MongoDB timeout must be positive")
    .parse(environment[MONGODB_TIMEOUT_ENV]?.trim() || DEFAULT_MONGODB_TIMEOUT);
  return Object.freeze({ enabled: true, uri, database, timeoutMs });
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
