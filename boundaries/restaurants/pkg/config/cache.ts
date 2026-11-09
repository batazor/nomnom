import { z } from "zod";

import {
  DEFAULT_CACHE_REFRESH_GROUP,
  DEFAULT_CACHE_TTL,
  DEFAULT_VALKEY_COMMAND_TIMEOUT,
} from "./defaults.ts";
import type { Spec } from "./file.ts";
import { positiveDurationSchema } from "./schemas.ts";

/** Secret: URL ACL-пользователя Valkey, только из env (Kubernetes Secret). */
export const VALKEY_URL_ENV = "VALKEY__URL";

/** `spec.valkey`: подключение к Valkey без URL, который приходит из Secret. */
export const valkeySectionSchema = z
  .strictObject({
    commandTimeout: positiveDurationSchema.prefault(
      DEFAULT_VALKEY_COMMAND_TIMEOUT,
    ),
  })
  .prefault({});

/** `spec.cache`: кеш чтения каталога. */
export const cacheSectionSchema = z
  .strictObject({
    ttl: positiveDurationSchema.prefault(DEFAULT_CACHE_TTL),
    refreshGroup: z
      .string()
      .trim()
      .regex(/^[A-Za-z0-9._-]{1,249}$/, "expected a Kafka consumer group")
      .prefault(DEFAULT_CACHE_REFRESH_GROUP),
  })
  .prefault({});

/** Кеш чтения каталога в Valkey (ADR 43, локальный ADR 3); `VALKEY__URL` обязателен. */
export type ValkeyConfig = Readonly<{
  /** `redis://` или `rediss://` с ACL-пользователем контекста; приходит из Secret. */
  url: string;
  /**
   * Deadline одной команды. Истёк — чтение идёт в MongoDB мимо кеша, поэтому
   * значение должно быть заметно меньше времени чтения из базы.
   */
  commandTimeoutMs: number;
  /**
   * TTL записи: изменение без Integration Event (цена, название) видно не
   * позже чем через это время.
   */
  ttlMs: number;
  /** Consumer group обновления кеша по restaurant-events и menu-events. */
  refreshGroup: string;
}>;

export function loadValkeyConfig(
  spec: Pick<Spec, "valkey" | "cache">,
  environment: Readonly<Record<string, string | undefined>>,
): ValkeyConfig {
  const url = environment[VALKEY_URL_ENV]?.trim() ?? "";
  if (url === "") {
    throw new Error(
      `${VALKEY_URL_ENV} is required: restaurants caches catalog reads in Valkey`,
    );
  }
  if (!/^rediss?:\/\/[^/]/.test(url)) {
    throw new Error(
      `${VALKEY_URL_ENV} must be a redis:// or rediss:// connection string`,
    );
  }
  return Object.freeze({
    url,
    commandTimeoutMs: spec.valkey.commandTimeout,
    ttlMs: spec.cache.ttl,
    refreshGroup: spec.cache.refreshGroup,
  });
}
