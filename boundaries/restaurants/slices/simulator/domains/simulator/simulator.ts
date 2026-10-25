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

/** States of an accepted order whose next step the Simulator plans. */
export const PREPARATION_STAGES = [
  RestaurantOrderState.Queued,
  RestaurantOrderState.Preparing,
  RestaurantOrderState.Assembled,
] as const;

export type PreparationStage = (typeof PREPARATION_STAGES)[number];

/** Planned moment (epoch ms) of the step taken in `state`. */
export type PlannedStep = Readonly<{ state: PreparationStage; dueAt: number }>;

/**
 * Planned remaining Preparation of one order (FR-609): the steps in state
 * order; the step of `ASSEMBLED` (mark ready) is the planned `READY` moment.
 */
export type PreparationPlan = Readonly<{ steps: readonly PlannedStep[] }>;

/**
 * Plans the steps from `from` to `READY`: each step comes a random delay in
 * `[minMs, maxMs]` (at least 1 ms) after the previous one, starting at `now`.
 * `random` is called once per step.
 */
export const planPreparation = (
  from: PreparationStage,
  now: number,
  bounds: Readonly<{ minMs: number; maxMs: number }>,
  random: () => number,
): PreparationPlan => {
  const steps: PlannedStep[] = [];
  let dueAt = now;
  for (const state of PREPARATION_STAGES.slice(
    PREPARATION_STAGES.indexOf(from),
  )) {
    dueAt += Math.max(1, randomDelayMs(bounds, random()));
    steps.push({ state, dueAt });
  }
  return { steps };
};

/** Planned `READY` moment: the due time of the last step. */
export const plannedReadyAt = (plan: PreparationPlan): number => {
  const last = plan.steps.at(-1);
  if (last === undefined) throw new Error("preparation plan has no steps");
  return last.dueAt;
};

/** Moves every step by the same amount so `READY` lands at `readyAt`. */
export const shiftPlan = (
  plan: PreparationPlan,
  readyAt: number,
): PreparationPlan => {
  const shift = readyAt - plannedReadyAt(plan);
  return {
    steps: plan.steps.map((step) => ({ ...step, dueAt: step.dueAt + shift })),
  };
};
