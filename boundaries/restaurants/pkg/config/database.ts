import { z } from "zod";

import {
  DEFAULT_MONGODB_DATABASE,
  DEFAULT_MONGODB_MAX_CONNECTING,
  DEFAULT_MONGODB_MAX_IDLE_TIME,
  DEFAULT_MONGODB_MAX_POOL_SIZE,
  DEFAULT_MONGODB_MIN_POOL_SIZE,
  DEFAULT_MONGODB_TIMEOUT,
  DEFAULT_MONGODB_WAIT_QUEUE_TIMEOUT,
  MONGODB_POOL_SIZE_LIMIT,
} from "./defaults.ts";
import type { Spec } from "./file.ts";
import { positiveDurationSchema } from "./schemas.ts";

/** Secret: connection string replica set, только из env (Kubernetes Secret). */
export const MONGODB_URI_ENV = "MONGODB__URI";

/**
 * Processes of restaurants that open a MongoClient; each sizes its own pool
 * to its concurrency.
 */
export type Process = "api" | "consumer";

/**
 * `spec.mongodb`: база, timeout и лимиты пула без connection string из Secret.
 * 0 у таймаутов драйвер трактует как «без ограничения», поэтому он запрещён.
 */
export const mongodbSectionSchema = z
  .strictObject({
    database: z
      .string()
      .regex(/^[A-Za-z0-9_-]{1,63}$/, "invalid database name")
      .prefault(DEFAULT_MONGODB_DATABASE),
    timeout: positiveDurationSchema.prefault(DEFAULT_MONGODB_TIMEOUT),
    /** Пусто — default процесса (`DEFAULT_MONGODB_MAX_POOL_SIZE`). */
    maxPoolSize: z
      .number()
      .int()
      .min(1)
      .max(MONGODB_POOL_SIZE_LIMIT)
      .optional(),
    minPoolSize: z
      .number()
      .int()
      .min(0)
      .prefault(DEFAULT_MONGODB_MIN_POOL_SIZE),
    maxIdleTime: positiveDurationSchema.prefault(DEFAULT_MONGODB_MAX_IDLE_TIME),
    waitQueueTimeout: positiveDurationSchema.prefault(
      DEFAULT_MONGODB_WAIT_QUEUE_TIMEOUT,
    ),
    /** Пусто — `DEFAULT_MONGODB_MAX_CONNECTING`, но не больше maxPoolSize. */
    maxConnecting: z.number().int().min(1).optional(),
  })
  .prefault({});

export type MongoDbSection = z.output<typeof mongodbSectionSchema>;

/** Подключение к MongoDB (ADR 8, локальный ADR 1); `MONGODB__URI` обязателен. */
export type Config = Readonly<{
  /** Connection string replica set; приходит из Secret, не из файла конфигурации. */
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

export function load(
  spec: Pick<Spec, "mongodb">,
  environment: Readonly<Record<string, string | undefined>>,
  process: Process,
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
  return Object.freeze({
    uri,
    database: spec.mongodb.database,
    timeoutMs: spec.mongodb.timeout,
    pool: loadPool(spec.mongodb, process),
  });
}

/**
 * Лимиты пула с defaults процесса; минимум и число одновременных подключений
 * больше maxPoolSize останавливают старт.
 */
export function loadPool(
  mongodb: MongoDbSection,
  process: Process,
): PoolConfig {
  const maxPoolSize =
    mongodb.maxPoolSize ?? DEFAULT_MONGODB_MAX_POOL_SIZE[process];
  if (mongodb.minPoolSize > maxPoolSize) {
    throw new Error(
      `spec.mongodb.minPoolSize: ${mongodb.minPoolSize} must not exceed maxPoolSize ${maxPoolSize}`,
    );
  }
  // Default не превышает маленький пул (maxPoolSize: 1); явное значение
  // больше maxPoolSize бессмысленно и отклоняется.
  const maxConnecting =
    mongodb.maxConnecting ??
    Math.min(DEFAULT_MONGODB_MAX_CONNECTING, maxPoolSize);
  if (maxConnecting > maxPoolSize) {
    throw new Error(
      `spec.mongodb.maxConnecting: ${maxConnecting} must not exceed maxPoolSize ${maxPoolSize}`,
    );
  }
  return Object.freeze({
    maxPoolSize,
    minPoolSize: mongodb.minPoolSize,
    maxIdleTimeMs: mongodb.maxIdleTime,
    waitQueueTimeoutMs: mongodb.waitQueueTimeout,
    maxConnecting,
  });
}
