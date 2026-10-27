import { z } from "zod";
import {
  DEFAULT_WAIT_TIME_ELEVATED_FACTOR,
  DEFAULT_WAIT_TIME_NORMAL_FACTOR,
  DEFAULT_WAIT_TIME_RECENT_WINDOW,
  DEFAULT_WAIT_TIME_SAMPLE_SIZE,
  WAIT_TIME_ELEVATED_FACTOR_ENV,
  WAIT_TIME_NORMAL_FACTOR_ENV,
  WAIT_TIME_RECENT_WINDOW_ENV,
  WAIT_TIME_SAMPLE_SIZE_ENV,
} from "./env.ts";
import { durationSchema } from "./schemas.ts";

/**
 * Wait Time Statistics of a restaurant (FR-645, FR-657): sample of the
 * baseline, window of the recent median and the thresholds of the level.
 */
export interface WaitTimeConfig {
  readonly sampleSize: number;
  readonly recentWindowMs: number;
  /** ELEVATED when the current wait exceeds the baseline this many times. */
  readonly elevatedFactor: number;
  /** NORMAL again when the current wait is at most this many baselines. */
  readonly normalFactor: number;
}

const factorSchema = (name: string) =>
  z.coerce
    .number({ error: `${name}: expected a number` })
    .positive(`${name}: expected a positive number`);

export function loadWaitTimeConfig(
  environment: Record<string, string | undefined>,
): WaitTimeConfig {
  const sampleSize = z.coerce
    .number()
    .int()
    .min(1, `${WAIT_TIME_SAMPLE_SIZE_ENV}: expected a positive integer`)
    .parse(
      environment[WAIT_TIME_SAMPLE_SIZE_ENV]?.trim() ||
        DEFAULT_WAIT_TIME_SAMPLE_SIZE,
    );
  const recentWindowMs = durationSchema
    .refine((value) => value > 0, "recent window must be positive")
    .parse(
      environment[WAIT_TIME_RECENT_WINDOW_ENV]?.trim() ||
        DEFAULT_WAIT_TIME_RECENT_WINDOW,
    );
  const elevatedFactor = factorSchema(WAIT_TIME_ELEVATED_FACTOR_ENV).parse(
    environment[WAIT_TIME_ELEVATED_FACTOR_ENV]?.trim() ||
      DEFAULT_WAIT_TIME_ELEVATED_FACTOR,
  );
  const normalFactor = factorSchema(WAIT_TIME_NORMAL_FACTOR_ENV).parse(
    environment[WAIT_TIME_NORMAL_FACTOR_ENV]?.trim() ||
      DEFAULT_WAIT_TIME_NORMAL_FACTOR,
  );
  if (normalFactor >= elevatedFactor) {
    throw new Error(
      `${WAIT_TIME_NORMAL_FACTOR_ENV} must be less than ${WAIT_TIME_ELEVATED_FACTOR_ENV}`,
    );
  }
  return Object.freeze({
    sampleSize,
    recentWindowMs,
    elevatedFactor,
    normalFactor,
  });
}
