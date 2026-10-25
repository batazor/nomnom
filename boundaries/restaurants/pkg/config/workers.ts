import { z } from "zod";
import {
  DEFAULT_OUTBOX_BATCH_SIZE,
  DEFAULT_OUTBOX_LEASE_TTL,
  DEFAULT_OUTBOX_POLL_INTERVAL,
  DEFAULT_OUTBOX_RETENTION,
  DEFAULT_RECONCILER_GRACE,
  DEFAULT_RECONCILER_INTERVAL,
  DEFAULT_RECONCILER_PAGE_SIZE,
  OUTBOX_BATCH_SIZE_ENV,
  OUTBOX_LEASE_TTL_ENV,
  OUTBOX_POLL_INTERVAL_ENV,
  OUTBOX_RETENTION_ENV,
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

/** Polling relay Outbox (ADR 31). */
export interface OutboxRelayConfig {
  /** Опубликованные записи старше этого срока удаляются. */
  readonly retentionMs: number;
  readonly pollIntervalMs: number;
  readonly batchSize: number;
  /** Срок lease единственного активного relay; продлевается каждый цикл. */
  readonly leaseTtlMs: number;
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

export function loadOutboxRelayConfig(
  environment: Record<string, string | undefined>,
): OutboxRelayConfig {
  const positive = (name: string) =>
    durationSchema.refine((value) => value > 0, `${name} must be positive`);
  const retentionMs = positive(OUTBOX_RETENTION_ENV).parse(
    environment[OUTBOX_RETENTION_ENV]?.trim() || DEFAULT_OUTBOX_RETENTION,
  );
  const pollIntervalMs = positive(OUTBOX_POLL_INTERVAL_ENV).parse(
    environment[OUTBOX_POLL_INTERVAL_ENV]?.trim() ||
      DEFAULT_OUTBOX_POLL_INTERVAL,
  );
  const leaseTtlMs = positive(OUTBOX_LEASE_TTL_ENV).parse(
    environment[OUTBOX_LEASE_TTL_ENV]?.trim() || DEFAULT_OUTBOX_LEASE_TTL,
  );
  if (leaseTtlMs <= pollIntervalMs) {
    throw new Error(
      `${OUTBOX_LEASE_TTL_ENV} must be longer than ${OUTBOX_POLL_INTERVAL_ENV}`,
    );
  }
  const batchSize = z.coerce
    .number()
    .int()
    .min(1)
    .max(10_000)
    .parse(
      environment[OUTBOX_BATCH_SIZE_ENV]?.trim() || DEFAULT_OUTBOX_BATCH_SIZE,
    );
  return Object.freeze({ retentionMs, pollIntervalMs, batchSize, leaseTtlMs });
}
