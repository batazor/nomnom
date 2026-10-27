import { z } from "zod";
import {
  DEFAULT_MONGODB_DATABASE,
  DEFAULT_MONGODB_MAX_CONNECTING,
  DEFAULT_MONGODB_MAX_IDLE_TIME,
  DEFAULT_MONGODB_MAX_POOL_SIZE,
  DEFAULT_MONGODB_MIN_POOL_SIZE,
  DEFAULT_MONGODB_TIMEOUT,
  DEFAULT_MONGODB_WAIT_QUEUE_TIMEOUT,
  MONGODB_DATABASE_ENV,
  MONGODB_MAX_CONNECTING_ENV,
  MONGODB_MAX_IDLE_TIME_ENV,
  MONGODB_MAX_POOL_SIZE_ENV,
  MONGODB_MIN_POOL_SIZE_ENV,
  MONGODB_POOL_SIZE_LIMIT,
  MONGODB_TIMEOUT_ENV,
  MONGODB_URI_ENV,
  MONGODB_WAIT_QUEUE_TIMEOUT_ENV,
} from "./env.ts";
import { durationSchema } from "./schemas.ts";

/**
 * Processes of restaurants that open a MongoClient; each sizes its own pool
 * to its concurrency.
 */
export type Process = "api" | "consumer";

/** Подключение к MongoDB (ADR 8, локальный ADR 1), когда задан `MONGODB__URI`. */
export type Config = Readonly<{
  /** Connection string replica set; приходит из Secret, не из values. */
  uri: string;
  database: string;
  /** Server selection timeout: недоступная MongoDB останавливает старт. */
  timeoutMs: number;
  pool: PoolConfig;
}>;

/**
 * Connection pool MongoClient процесса (CMAP). Драйвер держит отдельный пул на
 * каждый сервер replica set, поэтому лимиты действуют на пул одного сервера.
 */
export type PoolConfig = Readonly<{
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

const MONGODB_DATABASE_NAME = /^[A-Za-z0-9_-]{1,63}$/;

export function load(
  environment: Record<string, string | undefined>,
  process: Process = "api",
): Config {
  const uri = environment[MONGODB_URI_ENV]?.trim() ?? "";
  if (uri === "") {
    throw new Error(
      `${MONGODB_URI_ENV} is required: restaurants stores its data in MongoDB`,
    );
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
    uri,
    database,
    timeoutMs,
    pool: loadPool(environment, process),
  });
}

/**
 * Лимиты пула с defaults процесса; значение вне диапазона останавливает старт.
 * 0 у таймаутов драйвер трактует как «без ограничения», поэтому он запрещён.
 */
export function loadPool(
  environment: Record<string, string | undefined>,
  process: Process = "api",
): PoolConfig {
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
