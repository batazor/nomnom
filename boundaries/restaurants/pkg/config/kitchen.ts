import { z } from "zod";
import {
  DEFAULT_KITCHEN_CANCELLATION_DURING_PREPARATION,
  DEFAULT_KITCHEN_PREPARATION_TIME,
  DEFAULT_KITCHEN_REPEAT_POLICY,
  KITCHEN_CANCELLATION_DURING_PREPARATION_ENV,
  KITCHEN_PREPARATION_TIME_ENV,
  KITCHEN_REPEAT_POLICY_ENV,
} from "./env.ts";
import { durationSchema } from "./schemas.ts";

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
}

export function loadKitchenConfig(
  environment: Record<string, string | undefined>,
): KitchenConfig {
  const preparationTimeMs = durationSchema
    .refine((value) => value > 0, "preparation time must be positive")
    .parse(
      environment[KITCHEN_PREPARATION_TIME_ENV]?.trim() ||
        DEFAULT_KITCHEN_PREPARATION_TIME,
    );
  const repeatPolicy = z
    .enum(["succeed-without-event", "reject"], {
      error: `${KITCHEN_REPEAT_POLICY_ENV}: expected succeed-without-event or reject`,
    })
    .parse(
      environment[KITCHEN_REPEAT_POLICY_ENV]?.trim() ||
        DEFAULT_KITCHEN_REPEAT_POLICY,
    );
  const cancellationDuringPreparation = z
    .enum(["allow", "reject"], {
      error: `${KITCHEN_CANCELLATION_DURING_PREPARATION_ENV}: expected allow or reject`,
    })
    .parse(
      environment[KITCHEN_CANCELLATION_DURING_PREPARATION_ENV]?.trim() ||
        DEFAULT_KITCHEN_CANCELLATION_DURING_PREPARATION,
    );
  return Object.freeze({
    preparationTimeMs,
    repeatPolicy,
    cancellationDuringPreparation,
  });
}
