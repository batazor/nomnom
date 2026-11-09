import { z } from "zod";

import type { Spec } from "./file.ts";
import { durationSchema, positiveDurationSchema } from "./schemas.ts";

/** `spec.simulator`: обязательна процессу restaurants, без defaults. */
export const simulatorSectionSchema = z
  .strictObject({
    interval: positiveDurationSchema.optional(),
    minDelay: durationSchema.optional(),
    maxDelay: durationSchema.optional(),
  })
  .prefault({});

/**
 * Demo Simulator of the kitchen (FR-605, FR-606). Every value is required:
 * a missing or invalid value, or `minDelay > maxDelay`, stops the startup.
 */
export interface SimulatorConfig {
  /** Delay between the end of one simulator pass and the start of the next. */
  readonly intervalMs: number;
  /** Bounds of the random delay of one action after the order is seen. */
  readonly minDelayMs: number;
  readonly maxDelayMs: number;
}

export function loadSimulatorConfig(
  spec: Pick<Spec, "simulator">,
): SimulatorConfig {
  const required = (name: keyof Spec["simulator"]): number => {
    const value = spec.simulator[name];
    if (value === undefined) {
      throw new Error(`spec.simulator.${name} is required`);
    }
    return value;
  };
  const intervalMs = required("interval");
  const minDelayMs = required("minDelay");
  const maxDelayMs = required("maxDelay");
  if (minDelayMs > maxDelayMs) {
    throw new Error(
      "spec.simulator.minDelay must not be longer than spec.simulator.maxDelay",
    );
  }
  return Object.freeze({ intervalMs, minDelayMs, maxDelayMs });
}
