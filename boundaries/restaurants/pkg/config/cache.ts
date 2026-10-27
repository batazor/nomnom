import {
  CACHE_REFRESH_GROUP_ENV,
  CACHE_TTL_ENV,
  DEFAULT_CACHE_REFRESH_GROUP,
  DEFAULT_CACHE_TTL,
  DEFAULT_VALKEY_COMMAND_TIMEOUT,
  VALKEY_COMMAND_TIMEOUT_ENV,
  VALKEY_URL_ENV,
} from "./env.ts";
import { durationSchema } from "./schemas.ts";

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
  environment: Record<string, string | undefined>,
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
  const commandTimeoutMs = durationSchema
    .refine((value) => value > 0, "Valkey command timeout must be positive")
    .parse(
      environment[VALKEY_COMMAND_TIMEOUT_ENV]?.trim() ||
        DEFAULT_VALKEY_COMMAND_TIMEOUT,
    );
  const ttlMs = durationSchema
    .refine((value) => value > 0, "cache TTL must be positive")
    .parse(environment[CACHE_TTL_ENV]?.trim() || DEFAULT_CACHE_TTL);
  const refreshGroup =
    environment[CACHE_REFRESH_GROUP_ENV]?.trim() || DEFAULT_CACHE_REFRESH_GROUP;
  return Object.freeze({
    url,
    commandTimeoutMs,
    ttlMs,
    refreshGroup,
  });
}
