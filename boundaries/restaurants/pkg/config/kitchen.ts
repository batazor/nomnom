import { z } from "zod";

import {
  DEFAULT_KITCHEN_CANCELLATION_DURING_PREPARATION,
  DEFAULT_KITCHEN_PREPARATION_TIME,
  DEFAULT_KITCHEN_QUEUE_AGING_FACTOR,
  DEFAULT_KITCHEN_REPEAT_POLICY,
} from "./defaults.ts";
import type { Spec } from "./file.ts";
import { positiveDurationSchema } from "./schemas.ts";

/** `spec.kitchen`: open product decisions of Kitchen Order commands. */
export const kitchenSectionSchema = z
  .strictObject({
    preparationTime: positiveDurationSchema.prefault(
      DEFAULT_KITCHEN_PREPARATION_TIME,
    ),
    repeatPolicy: z
      .enum(["succeed-without-event", "reject"], {
        error: "expected succeed-without-event or reject",
      })
      .prefault(DEFAULT_KITCHEN_REPEAT_POLICY),
    cancellationDuringPreparation: z
      .enum(["allow", "reject"], { error: "expected allow or reject" })
      .prefault(DEFAULT_KITCHEN_CANCELLATION_DURING_PREPARATION),
  })
  .prefault({});

/** `spec.kitchenQueue`: Kitchen Queue (FR-241). */
export const kitchenQueueSectionSchema = z
  .strictObject({
    agingFactor: z
      .number({ error: "expected a number" })
      .positive("expected a positive number")
      .prefault(DEFAULT_KITCHEN_QUEUE_AGING_FACTOR),
  })
  .prefault({});

/**
 * Open product decisions of Kitchen Order commands. Handlers take them as
 * required parameters without defaults; the defaults live here.
 */
export interface KitchenConfig {
  /**
   * Initial `expected_ready_at` after Acceptance; staff move it later with
   * Report Preparation Delay.
   */
  readonly preparationTimeMs: number;
  /**
   * A repeated Start Preparation or Mark Ready for Pickup: `succeed-without-event`
   * answers with the current order and publishes nothing, `reject` fails.
   */
  readonly repeatPolicy: "succeed-without-event" | "reject";
  /** Cancel Restaurant Order during Preparation: `reject` keeps it going. */
  readonly cancellationDuringPreparation: "allow" | "reject";
  /**
   * Kitchen Queue (FR-241): a standard order waiting longer than this many
   * baseline waits of the restaurant is taken together with premium orders.
   */
  readonly queueAgingFactor: number;
}

export function loadKitchenConfig(
  spec: Pick<Spec, "kitchen" | "kitchenQueue">,
): KitchenConfig {
  return Object.freeze({
    preparationTimeMs: spec.kitchen.preparationTime,
    repeatPolicy: spec.kitchen.repeatPolicy,
    cancellationDuringPreparation: spec.kitchen.cancellationDuringPreparation,
    queueAgingFactor: spec.kitchenQueue.agingFactor,
  });
}
