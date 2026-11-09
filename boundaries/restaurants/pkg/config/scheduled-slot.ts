import { z } from "zod";

import type { Spec } from "./file.ts";
import { positiveDurationSchema } from "./schemas.ts";

const HALF_HOUR_MS = 30 * 60_000;

/** `spec.scheduledSlot`: обязательна процессу restaurants, без defaults. */
export const scheduledSlotSectionSchema = z
  .strictObject({
    width: positiveDurationSchema.optional(),
    minLead: positiveDurationSchema.optional(),
    maxHorizon: positiveDurationSchema.optional(),
    holdTtl: positiveDurationSchema.optional(),
    alternatives: z
      .number({ error: "expected a number" })
      .int("expected an integer")
      .min(0, "expected zero or more")
      .optional(),
  })
  .prefault({});

/**
 * Delivery Windows of scheduled orders (FR-694). Every value is required: a
 * missing or invalid value, a width that does not divide 30 minutes, or
 * `minLead >= maxHorizon` stops the startup.
 */
export interface ScheduledSlotConfig {
  /**
   * Width of a Delivery Window; the grid is aligned to UTC, and a width
   * dividing 30 minutes keeps local window bounds on the grid at every
   * offset, before and after a clock change.
   */
  readonly widthMs: number;
  /** The first window starts no earlier than now + minLead. */
  readonly minLeadMs: number;
  /**
   * Windows start before now + maxHorizon. MUST be shorter than the
   * Authorization hold of the payment provider.
   */
  readonly maxHorizonMs: number;
  /** A held Slot Reservation expires this long after it was made. */
  readonly holdTtlMs: number;
  /** Nearest available windows offered when a window is full. */
  readonly alternatives: number;
}

export function loadScheduledSlotConfig(
  spec: Pick<Spec, "scheduledSlot">,
): ScheduledSlotConfig {
  const required = (name: keyof Spec["scheduledSlot"]): number => {
    const value = spec.scheduledSlot[name];
    if (value === undefined) {
      throw new Error(`spec.scheduledSlot.${name} is required`);
    }
    return value;
  };
  const widthMs = required("width");
  if (HALF_HOUR_MS % widthMs !== 0) {
    throw new Error("spec.scheduledSlot.width must divide 30 minutes");
  }
  const minLeadMs = required("minLead");
  const maxHorizonMs = required("maxHorizon");
  if (minLeadMs >= maxHorizonMs) {
    throw new Error(
      "spec.scheduledSlot.minLead must be shorter than spec.scheduledSlot.maxHorizon",
    );
  }
  return Object.freeze({
    widthMs,
    minLeadMs,
    maxHorizonMs,
    holdTtlMs: required("holdTtl"),
    alternatives: required("alternatives"),
  });
}
