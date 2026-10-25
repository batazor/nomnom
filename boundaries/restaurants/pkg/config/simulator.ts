import {
  SIMULATOR_INTERVAL_ENV,
  SIMULATOR_MAX_DELAY_ENV,
  SIMULATOR_MIN_DELAY_ENV,
} from "./env.ts";
import { durationSchema } from "./schemas.ts";

/**
 * Demo Simulator of the kitchen (FR-605, FR-606). Every value is required:
 * an empty or invalid value, or `MIN_DELAY > MAX_DELAY`, stops the startup.
 */
export interface SimulatorConfig {
  /** Delay between the end of one simulator pass and the start of the next. */
  readonly intervalMs: number;
  /** Bounds of the random delay of one action after the order is seen. */
  readonly minDelayMs: number;
  readonly maxDelayMs: number;
}

export function loadSimulatorConfig(
  environment: Record<string, string | undefined>,
): SimulatorConfig {
  const duration = (name: string): number => {
    const value = environment[name]?.trim() ?? "";
    if (value === "") throw new Error(`${name} is required`);
    const parsed = durationSchema.safeParse(value);
    if (!parsed.success) {
      throw new Error(`${name}: ${parsed.error.issues[0]?.message}`);
    }
    return parsed.data;
  };
  const intervalMs = duration(SIMULATOR_INTERVAL_ENV);
  if (intervalMs <= 0) {
    throw new Error(`${SIMULATOR_INTERVAL_ENV} must be positive`);
  }
  const minDelayMs = duration(SIMULATOR_MIN_DELAY_ENV);
  const maxDelayMs = duration(SIMULATOR_MAX_DELAY_ENV);
  if (minDelayMs > maxDelayMs) {
    throw new Error(
      `${SIMULATOR_MIN_DELAY_ENV} must not be longer than ${SIMULATOR_MAX_DELAY_ENV}`,
    );
  }
  return Object.freeze({ intervalMs, minDelayMs, maxDelayMs });
}
