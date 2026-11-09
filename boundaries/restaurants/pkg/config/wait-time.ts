import { z } from "zod";

import {
  DEFAULT_WAIT_TIME_ELEVATED_FACTOR,
  DEFAULT_WAIT_TIME_NORMAL_FACTOR,
  DEFAULT_WAIT_TIME_RECENT_WINDOW,
  DEFAULT_WAIT_TIME_SAMPLE_SIZE,
} from "./defaults.ts";
import type { Spec } from "./file.ts";
import { positiveDurationSchema } from "./schemas.ts";

const factorSchema = z
  .number({ error: "expected a number" })
  .positive("expected a positive number");

/** `spec.waitTime`: Wait Time Statistics (FR-645, FR-657). */
export const waitTimeSectionSchema = z
  .strictObject({
    sampleSize: z
      .number()
      .int()
      .min(1, "expected a positive integer")
      .prefault(DEFAULT_WAIT_TIME_SAMPLE_SIZE),
    recentWindow: positiveDurationSchema.prefault(
      DEFAULT_WAIT_TIME_RECENT_WINDOW,
    ),
    elevatedFactor: factorSchema.prefault(DEFAULT_WAIT_TIME_ELEVATED_FACTOR),
    normalFactor: factorSchema.prefault(DEFAULT_WAIT_TIME_NORMAL_FACTOR),
  })
  .prefault({});

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

export function loadWaitTimeConfig(
  spec: Pick<Spec, "waitTime">,
): WaitTimeConfig {
  const { waitTime } = spec;
  if (waitTime.normalFactor >= waitTime.elevatedFactor) {
    throw new Error(
      "spec.waitTime.normalFactor must be less than spec.waitTime.elevatedFactor",
    );
  }
  return Object.freeze({
    sampleSize: waitTime.sampleSize,
    recentWindowMs: waitTime.recentWindow,
    elevatedFactor: waitTime.elevatedFactor,
    normalFactor: waitTime.normalFactor,
  });
}
