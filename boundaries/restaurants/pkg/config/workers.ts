import { z } from "zod";
import {
  CONSUMER_MAX_RETRIES_ENV,
  CONSUMER_RETRY_INITIAL_INTERVAL_ENV,
  CONSUMER_RETRY_MAX_INTERVAL_ENV,
  DEFAULT_CONSUMER_MAX_RETRIES,
  DEFAULT_CONSUMER_RETRY_INITIAL_INTERVAL,
  DEFAULT_CONSUMER_RETRY_MAX_INTERVAL,
  DEFAULT_KAFKA_CONSUMER_GROUP,
  DEFAULT_KAFKA_INITIAL_OFFSET,
  DEFAULT_RECONCILER_GRACE,
  DEFAULT_RECONCILER_INTERVAL,
  DEFAULT_RECONCILER_PAGE_SIZE,
  KAFKA_CONSUMER_GROUP_ENV,
  KAFKA_INITIAL_OFFSET_ENV,
  RECONCILER_GRACE_ENV,
  RECONCILER_INTERVAL_ENV,
  RECONCILER_PAGE_SIZE_ENV,
} from "./env.ts";
import { durationSchema } from "./schemas.ts";

/**
 * Background reconciliation of `restaurant#owner`/`staff` with the stored
 * restaurants (FR-445); runs inside the main process.
 */
export interface ReconcilerConfig {
  readonly intervalMs: number;
  /** An orphan relationship is deleted only after staying orphan this long. */
  readonly graceMs: number;
  /** ReadRelationships page size. */
  readonly pageSize: number;
}

/** Kafka consumer restaurants-consumer (ADR 11, ADR 28). */
export interface ConsumerConfig {
  /** Consumer group; общая для всех реплик, offsets хранит Kafka. */
  readonly groupId: string;
  /** `true` — новая группа читает стримы с начала (`oldest`). */
  readonly fromBeginning: boolean;
  /** Повторы transient-ошибки внутри процесса до повторной доставки Kafka. */
  readonly maxRetries: number;
  readonly retryInitialIntervalMs: number;
  readonly retryMaxIntervalMs: number;
}

export function loadReconcilerConfig(
  environment: Record<string, string | undefined>,
): ReconcilerConfig {
  const intervalMs = durationSchema
    .refine((value) => value > 0, "reconciler interval must be positive")
    .parse(
      environment[RECONCILER_INTERVAL_ENV]?.trim() ||
        DEFAULT_RECONCILER_INTERVAL,
    );
  const graceMs = durationSchema
    .refine((value) => value > 0, "reconciler grace must be positive")
    .parse(
      environment[RECONCILER_GRACE_ENV]?.trim() || DEFAULT_RECONCILER_GRACE,
    );
  // SpiceDB caps ReadRelationships at 1000 relationships per page by default.
  const pageSize = z.coerce
    .number()
    .int()
    .min(1)
    .max(1000)
    .parse(
      environment[RECONCILER_PAGE_SIZE_ENV]?.trim() ||
        DEFAULT_RECONCILER_PAGE_SIZE,
    );
  return Object.freeze({ intervalMs, graceMs, pageSize });
}

const CONSUMER_GROUP = /^[A-Za-z0-9._-]{1,249}$/;

export function loadConsumerConfig(
  environment: Record<string, string | undefined>,
): ConsumerConfig {
  const groupId =
    environment[KAFKA_CONSUMER_GROUP_ENV]?.trim() ||
    DEFAULT_KAFKA_CONSUMER_GROUP;
  if (!CONSUMER_GROUP.test(groupId)) {
    throw new Error(
      `${KAFKA_CONSUMER_GROUP_ENV}: invalid consumer group "${groupId}"`,
    );
  }
  const initialOffset = z
    .enum(["oldest", "newest"], { error: "expected oldest or newest" })
    .parse(
      environment[KAFKA_INITIAL_OFFSET_ENV]?.trim() ||
        DEFAULT_KAFKA_INITIAL_OFFSET,
    );
  const maxRetries = z.coerce
    .number()
    .int()
    .min(0)
    .max(100)
    .parse(
      environment[CONSUMER_MAX_RETRIES_ENV]?.trim() ||
        DEFAULT_CONSUMER_MAX_RETRIES,
    );
  const positive = (name: string) =>
    durationSchema.refine((value) => value > 0, `${name} must be positive`);
  const retryInitialIntervalMs = positive(
    CONSUMER_RETRY_INITIAL_INTERVAL_ENV,
  ).parse(
    environment[CONSUMER_RETRY_INITIAL_INTERVAL_ENV]?.trim() ||
      DEFAULT_CONSUMER_RETRY_INITIAL_INTERVAL,
  );
  const retryMaxIntervalMs = positive(CONSUMER_RETRY_MAX_INTERVAL_ENV).parse(
    environment[CONSUMER_RETRY_MAX_INTERVAL_ENV]?.trim() ||
      DEFAULT_CONSUMER_RETRY_MAX_INTERVAL,
  );
  if (retryMaxIntervalMs < retryInitialIntervalMs) {
    throw new Error(
      `${CONSUMER_RETRY_MAX_INTERVAL_ENV} must not be shorter than ${CONSUMER_RETRY_INITIAL_INTERVAL_ENV}`,
    );
  }
  return Object.freeze({
    groupId,
    fromBeginning: initialOffset === "oldest",
    maxRetries,
    retryInitialIntervalMs,
    retryMaxIntervalMs,
  });
}
