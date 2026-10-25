// Demo Simulator of the kitchen (FR-604, FR-605): the persistent on/off state
// and the weighted choice of the action the simulated restaurant owner takes
// on a Restaurant Order waiting for the kitchen.
import { RestaurantOrderState } from "../../../restaurantorder/domains/restaurantorder/restaurantorder.ts";

/**
 * Stored Simulator state of this service; a service without a stored state
 * is disabled. Survives restarts.
 */
export interface SimulatorStateRepository {
  isEnabled(): Promise<boolean>;
  setEnabled(enabled: boolean, changedAt: Date): Promise<void>;
}

/** Kitchen action the Simulator performs through the real use case. */
export type KitchenAction =
  | "accept"
  | "reject"
  | "start_preparation"
  | "report_delay"
  | "mark_assembled"
  | "report_failure"
  | "mark_ready";

/** States in which the restaurant owner is expected to act. */
export const SIMULATED_STATES = [
  RestaurantOrderState.Received,
  RestaurantOrderState.Queued,
  RestaurantOrderState.Preparing,
  RestaurantOrderState.Assembled,
] as const;

export type SimulatedState = (typeof SIMULATED_STATES)[number];

/** FR-605: outcome weights by state, in percent. */
export const KITCHEN_ACTION_WEIGHTS: Readonly<
  Record<SimulatedState, readonly (readonly [KitchenAction, number])[]>
> = {
  [RestaurantOrderState.Received]: [
    ["accept", 90],
    ["reject", 10],
  ],
  [RestaurantOrderState.Queued]: [
    ["start_preparation", 90],
    ["report_delay", 10],
  ],
  [RestaurantOrderState.Preparing]: [
    ["mark_assembled", 92],
    ["report_failure", 5],
    ["report_delay", 3],
  ],
  [RestaurantOrderState.Assembled]: [["mark_ready", 100]],
};

/**
 * Picks the action for `state` by its weights; `random` is a number in
 * `[0, 1)`.
 */
export const chooseKitchenAction = (
  state: SimulatedState,
  random: number,
): KitchenAction => {
  const weights = KITCHEN_ACTION_WEIGHTS[state];
  const total = weights.reduce((sum, [, weight]) => sum + weight, 0);
  let point = random * total;
  for (const [action, weight] of weights) {
    if (point < weight) return action;
    point -= weight;
  }
  throw new Error(`random ${random} is outside [0, 1)`);
};

/** Random delay in `[minMs, maxMs]`; `random` is a number in `[0, 1)`. */
export const randomDelayMs = (
  bounds: Readonly<{ minMs: number; maxMs: number }>,
  random: number,
): number => bounds.minMs + Math.round(random * (bounds.maxMs - bounds.minMs));
