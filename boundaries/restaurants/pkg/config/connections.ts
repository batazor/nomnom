import { z } from "zod";
import {
  type DatabaseProcess,
  DEFAULT_KAFKA_CLIENT_ID,
  DEFAULT_MONGODB_DATABASE,
  DEFAULT_MONGODB_MAX_CONNECTING,
  DEFAULT_MONGODB_MAX_IDLE_TIME,
  DEFAULT_MONGODB_MAX_POOL_SIZE,
  DEFAULT_MONGODB_MIN_POOL_SIZE,
  DEFAULT_MONGODB_TIMEOUT,
  DEFAULT_MONGODB_WAIT_QUEUE_TIMEOUT,
  DEFAULT_SPICEDB_TIMEOUT,
  KAFKA_BROKERS_ENV,
  KAFKA_CLIENT_ID_ENV,
  MONGODB_DATABASE_ENV,
  MONGODB_MAX_CONNECTING_ENV,
  MONGODB_MAX_IDLE_TIME_ENV,
  MONGODB_MAX_POOL_SIZE_ENV,
  MONGODB_MIN_POOL_SIZE_ENV,
  MONGODB_POOL_SIZE_LIMIT,
  MONGODB_TIMEOUT_ENV,
  MONGODB_URI_ENV,
  MONGODB_WAIT_QUEUE_TIMEOUT_ENV,
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

/** Подключение к MongoDB (ADR 8, локальный ADR 1), когда задан `MONGODB__URI`. */
export type DatabaseEnabledConfig = Readonly<{
  enabled: true;
  /** Connection string replica set; приходит из Secret, не из values. */
  uri: string;
  database: string;
  /** Server selection timeout: недоступная MongoDB останавливает старт. */
  timeoutMs: number;
  pool: DatabasePoolConfig;
}>;

/**
 * Connection pool MongoClient процесса (CMAP). Драйвер держит отдельный пул на
 * каждый сервер replica set, поэтому лимиты действуют на пул одного сервера.
 */
export type DatabasePoolConfig = Readonly<{
  /** Соединений в пуле (in use + idle), `maxPoolSize`. */
  maxPoolSize: number;
  /** Прогретые соединения, `minPoolSize`; 0 — пул растёт по требованию. */
  minPoolSize: number;
  /** Idle-соединение закрывается после этого срока, `maxIdleTimeMS`. */
  maxIdleTimeMs: number;
  /** Ожидание свободного соединения до ошибки, `waitQueueTimeoutMS`. */
  waitQueueTimeoutMs: number;
  /** Одновременно устанавливаемые соединения, `maxConnecting`. */
  maxConnecting: number;
}>;

/**
 * `enabled: false` при пустом `MONGODB__URI`; процессы `restaurants` и
 * `restaurants-consumer` в этом случае не стартуют.
 */
export type DatabaseConfig =
  | Readonly<{ enabled: false }>
  | DatabaseEnabledConfig;

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

const MONGODB_DATABASE_NAME = /^[A-Za-z0-9_-]{1,63}$/;

export function loadDatabaseConfig(
  environment: Record<string, string | undefined>,
  process: DatabaseProcess = "api",
): DatabaseConfig {
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
  return Object.freeze({
    enabled: true,
    uri,
    database,
    timeoutMs,
    pool: loadDatabasePoolConfig(environment, process),
  });
}

/**
 * Лимиты пула с defaults процесса; значение вне диапазона останавливает старт.
 * 0 у таймаутов драйвер трактует как «без ограничения», поэтому он запрещён.
 */
export function loadDatabasePoolConfig(
  environment: Record<string, string | undefined>,
  process: DatabaseProcess = "api",
): DatabasePoolConfig {
  const value = (name: string) => environment[name]?.trim() || undefined;
  const integer = (name: string, min: number, max: number, fallback: number) =>
    parseSetting(
      name,
      z.coerce.number().int().min(min).max(max),
      value(name) ?? fallback,
    );
  const positiveDuration = (name: string, fallback: string) =>
    parseSetting(
      name,
      durationSchema.refine((ms) => ms > 0, "must be positive"),
      value(name) ?? fallback,
    );

  const maxPoolSize = integer(
    MONGODB_MAX_POOL_SIZE_ENV,
    1,
    MONGODB_POOL_SIZE_LIMIT,
    DEFAULT_MONGODB_MAX_POOL_SIZE[process],
  );
  const minPoolSize = integer(
    MONGODB_MIN_POOL_SIZE_ENV,
    0,
    maxPoolSize,
    DEFAULT_MONGODB_MIN_POOL_SIZE,
  );
  // Default не превышает маленький пул (MONGODB__MAX_POOL_SIZE=1); явное
  // значение больше maxPoolSize бессмысленно и отклоняется.
  const maxConnecting = integer(
    MONGODB_MAX_CONNECTING_ENV,
    1,
    maxPoolSize,
    Math.min(DEFAULT_MONGODB_MAX_CONNECTING, maxPoolSize),
  );
  return Object.freeze({
    maxPoolSize,
    minPoolSize,
    maxIdleTimeMs: positiveDuration(
      MONGODB_MAX_IDLE_TIME_ENV,
      DEFAULT_MONGODB_MAX_IDLE_TIME,
    ),
    waitQueueTimeoutMs: positiveDuration(
      MONGODB_WAIT_QUEUE_TIMEOUT_ENV,
      DEFAULT_MONGODB_WAIT_QUEUE_TIMEOUT,
    ),
    maxConnecting,
  });
}

function parseSetting<T>(
  name: string,
  schema: z.ZodType<T>,
  raw: string | number,
): T {
  const result = schema.safeParse(raw);
  if (!result.success) {
    const reason = result.error.issues.map((issue) => issue.message).join("; ");
    throw new Error(`${name}: invalid value "${raw}": ${reason}`);
  }
  return result.data;
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
