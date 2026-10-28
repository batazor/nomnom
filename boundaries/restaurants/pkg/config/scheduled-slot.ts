import { z } from "zod";
import {
  SCHEDULED_SLOT_ALTERNATIVES_ENV,
  SCHEDULED_SLOT_HOLD_TTL_ENV,
  SCHEDULED_SLOT_MAX_HORIZON_ENV,
  SCHEDULED_SLOT_MIN_LEAD_ENV,
  SCHEDULED_SLOT_WIDTH_ENV,
} from "./env.ts";
import { durationSchema } from "./schemas.ts";

const HALF_HOUR_MS = 30 * 60_000;

/**
 * Delivery Windows of scheduled orders (FR-694). Every value is required: an
 * empty or invalid value, a width that does not divide 30 minutes, or
 * `MIN_LEAD >= MAX_HORIZON` stops the startup.
 */
export interface ScheduledSlotConfig {
  /**
   * Width of a Delivery Window; the grid is aligned to UTC, and a width
   * dividing 30 minutes keeps local window bounds on the grid at every
   * offset, before and after a clock change.
   */
  readonly widthMs: number;
  /** The first window starts no earlier than now + MIN_LEAD. */
  readonly minLeadMs: number;
  /**
   * Windows start before now + MAX_HORIZON. MUST be shorter than the
   * Authorization hold of the payment provider.
   */
  readonly maxHorizonMs: number;
  /** A held Slot Reservation expires this long after it was made. */
  readonly holdTtlMs: number;
  /** Nearest available windows offered when a window is full. */
  readonly alternatives: number;
}

export function loadScheduledSlotConfig(
  environment: Record<string, string | undefined>,
): ScheduledSlotConfig {
  const required = (name: string): string => {
    const value = environment[name]?.trim() ?? "";
    if (value === "") throw new Error(`${name} is required`);
    return value;
  };
  const duration = (name: string): number => {
    const parsed = durationSchema.safeParse(required(name));
    if (!parsed.success) {
      throw new Error(`${name}: ${parsed.error.issues[0]?.message}`);
    }
    if (parsed.data <= 0) throw new Error(`${name} must be positive`);
    return parsed.data;
  };
  const widthMs = duration(SCHEDULED_SLOT_WIDTH_ENV);
  if (HALF_HOUR_MS % widthMs !== 0) {
    throw new Error(`${SCHEDULED_SLOT_WIDTH_ENV} must divide 30 minutes`);
  }
  const minLeadMs = duration(SCHEDULED_SLOT_MIN_LEAD_ENV);
  const maxHorizonMs = duration(SCHEDULED_SLOT_MAX_HORIZON_ENV);
  if (minLeadMs >= maxHorizonMs) {
    throw new Error(
      `${SCHEDULED_SLOT_MIN_LEAD_ENV} must be shorter than ${SCHEDULED_SLOT_MAX_HORIZON_ENV}`,
    );
  }
  const holdTtlMs = duration(SCHEDULED_SLOT_HOLD_TTL_ENV);
  const alternatives = z.coerce
    .number({ error: `${SCHEDULED_SLOT_ALTERNATIVES_ENV}: expected a number` })
    .int(`${SCHEDULED_SLOT_ALTERNATIVES_ENV}: expected an integer`)
    .min(0, `${SCHEDULED_SLOT_ALTERNATIVES_ENV}: expected zero or more`)
    .parse(required(SCHEDULED_SLOT_ALTERNATIVES_ENV));
  return Object.freeze({
    widthMs,
    minLeadMs,
    maxHorizonMs,
    holdTtlMs,
    alternatives,
  });
}
