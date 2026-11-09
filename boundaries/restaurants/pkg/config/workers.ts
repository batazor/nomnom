import { z } from "zod";

import {
  DEFAULT_CONSUMER_MAX_RETRIES,
  DEFAULT_CONSUMER_RETRY_INITIAL_INTERVAL,
  DEFAULT_CONSUMER_RETRY_MAX_INTERVAL,
  DEFAULT_RECONCILER_GRACE,
  DEFAULT_RECONCILER_INTERVAL,
  DEFAULT_RECONCILER_PAGE_SIZE,
} from "./defaults.ts";
import type { Spec } from "./file.ts";
import { positiveDurationSchema } from "./schemas.ts";

/** `spec.reconciler`: reconciliation of `restaurant#owner`/`staff` (FR-445). */
export const reconcilerSectionSchema = z
  .strictObject({
    interval: positiveDurationSchema.prefault(DEFAULT_RECONCILER_INTERVAL),
    grace: positiveDurationSchema.prefault(DEFAULT_RECONCILER_GRACE),
    // SpiceDB caps ReadRelationships at 1000 relationships per page by default.
    pageSize: z
      .number()
      .int()
      .min(1)
      .max(1000)
      .prefault(DEFAULT_RECONCILER_PAGE_SIZE),
  })
  .prefault({});

/** `spec.consumer`: in-process retries of restaurants-consumer. */
export const consumerSectionSchema = z
  .strictObject({
    maxRetries: z
      .number()
      .int()
      .min(0)
      .max(100)
      .prefault(DEFAULT_CONSUMER_MAX_RETRIES),
    retryInitialInterval: positiveDurationSchema.prefault(
      DEFAULT_CONSUMER_RETRY_INITIAL_INTERVAL,
    ),
    retryMaxInterval: positiveDurationSchema.prefault(
      DEFAULT_CONSUMER_RETRY_MAX_INTERVAL,
    ),
  })
  .prefault({});

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
  spec: Pick<Spec, "reconciler">,
): ReconcilerConfig {
  return Object.freeze({
    intervalMs: spec.reconciler.interval,
    graceMs: spec.reconciler.grace,
    pageSize: spec.reconciler.pageSize,
  });
}

export function loadConsumerConfig(
  spec: Pick<Spec, "kafka" | "consumer">,
): ConsumerConfig {
  const { consumer } = spec;
  if (consumer.retryMaxInterval < consumer.retryInitialInterval) {
    throw new Error(
      "spec.consumer.retryMaxInterval must not be shorter than spec.consumer.retryInitialInterval",
    );
  }
  return Object.freeze({
    groupId: spec.kafka.consumerGroup,
    fromBeginning: spec.kafka.initialOffset === "oldest",
    maxRetries: consumer.maxRetries,
    retryInitialIntervalMs: consumer.retryInitialInterval,
    retryMaxIntervalMs: consumer.retryMaxInterval,
  });
}
