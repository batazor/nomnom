import { randomUUID } from "node:crypto";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { Clock } from "../../../../pkg/usecase/clock.ts";
import type { AcceptOrderHandler } from "../../../restaurantorder/applications/acceptance/accept-order/accept-order.ts";
import type { RejectOrderHandler } from "../../../restaurantorder/applications/acceptance/reject-order/reject-order.ts";
import type { MarkAssembledHandler } from "../../../restaurantorder/applications/preparation/mark-assembled/mark-assembled.ts";
import type { MarkReadyForPickupHandler } from "../../../restaurantorder/applications/preparation/mark-ready-for-pickup/mark-ready-for-pickup.ts";
import type { ReportPreparationDelayHandler } from "../../../restaurantorder/applications/preparation/report-preparation-delay/report-preparation-delay.ts";
import type { ReportPreparationFailureHandler } from "../../../restaurantorder/applications/preparation/report-preparation-failure/report-preparation-failure.ts";
import type { StartPreparationHandler } from "../../../restaurantorder/applications/preparation/start-preparation/start-preparation.ts";
import {
  NoCookAvailableError,
  RestaurantOrderState,
} from "../../../restaurantorder/domains/restaurantorder/restaurantorder.ts";
import {
  chooseKitchenAction,
  type KitchenAction,
  type PreparationPlan,
  plannedReadyAt,
  planPreparation,
  randomDelayMs,
  type SimulatedState,
  type SimulatorStateRepository,
  shiftPlan,
} from "../../domains/simulator/simulator.ts";

/**
 * Restaurant Order waiting for an action of its restaurant. `marketId` is the
 * stored Market of the order (ADR 41): the Simulator never names a Market of
 * its own, it only drives existing orders through the kitchen commands.
 */
export type PendingKitchenOrder = Readonly<{
  restaurantOrderId: string;
  restaurantId: string;
  marketId: string;
  state: SimulatedState;
  expectedReadyAt?: Date;
  /**
   * A scheduled order (FR-683): accepted without a time of the Simulator,
   * it stays ACCEPTED until `orders` queues it at its planned start.
   */
  scheduled: boolean;
}>;

/** Read model over all restaurants: orders in `SIMULATED_STATES`. */
export interface PendingKitchenOrdersReader {
  listPendingKitchenOrders(): Promise<readonly PendingKitchenOrder[]>;
}

/** Owner user of a restaurant; `undefined` when the restaurant is unknown. */
export interface RestaurantOwners {
  ownerOf(restaurantId: string): Promise<string | undefined>;
}

/** Real kitchen command handlers the Simulator calls as the owner. */
export type SimulatedKitchenCommands = Readonly<{
  accept: Pick<AcceptOrderHandler, "execute">;
  reject: Pick<RejectOrderHandler, "execute">;
  startPreparation: Pick<StartPreparationHandler, "execute">;
  markAssembled: Pick<MarkAssembledHandler, "execute">;
  markReady: Pick<MarkReadyForPickupHandler, "execute">;
  reportDelay: Pick<ReportPreparationDelayHandler, "execute">;
  reportFailure: Pick<ReportPreparationFailureHandler, "execute">;
}>;

/** Opaque reasons of simulated outcomes, as kitchen staff would report them. */
export const SIMULATED_REJECTION_REASON = "KITCHEN_BUSY";
export const SIMULATED_DELAY_REASON = "KITCHEN_BUSY";
export const SIMULATED_FAILURE_REASON = "ITEM_UNAVAILABLE";

/**
 * In-process schedule entry of one order: the due time of the accept/reject
 * decision of a `RECEIVED` order, or the plan of an accepted order's
 * remaining Preparation.
 */
type ScheduleEntry =
  | Readonly<{ kind: "decision"; dueAt: number }>
  | Readonly<{ kind: "preparation"; plan: PreparationPlan }>;

type KitchenOrderCommand = Readonly<{
  subject: Subject;
  restaurantId: string;
  restaurantOrderId: string;
}>;

/**
 * One pass of the kitchen Simulator (FR-605, FR-609). Disabled: forgets the
 * schedule and does nothing (AC-764). Enabled: a `RECEIVED` order is decided
 * `firstSeen + random [minDelay, maxDelay]`; accepting plans the whole
 * Preparation upfront (a random delay per step) and passes the planned
 * `READY` moment as `expectedReadyAt`; later steps run at their planned
 * moments. A delay moves `expectedReadyAt` and shifts the remaining plan to
 * it. A scheduled order is accepted without a time of its own: it is ready
 * at its Planned Ready At and leaves the Simulator until `orders` queues it.
 * An accepted order without a plan (restart, or accepted by a person) is
 * planned from now, and a planned `READY` later than its `expectedReadyAt` is
 * reported as a delay. Start Preparation waits for a free cook of the Cook
 * Pool: without one the step stays due for the next pass, and the remaining
 * steps follow the actual start (FR-618). Every action goes through the real
 * use case on behalf
 * of the restaurant owner, so the normal `operate_kitchen` check applies. A
 * failed action is logged and its entry dropped; the pass continues.
 */
export class KitchenSimulator {
  readonly #state: SimulatorStateRepository;
  readonly #orders: PendingKitchenOrdersReader;
  readonly #owners: RestaurantOwners;
  readonly #commands: SimulatedKitchenCommands;
  readonly #clock: Clock;
  readonly #random: () => number;
  readonly #delay: Readonly<{ minMs: number; maxMs: number }>;
  readonly #log: (message: string, fields?: Record<string, unknown>) => void;
  /** Keyed by `restaurantOrderId`; in-process only. */
  readonly #schedule = new Map<string, ScheduleEntry>();

  constructor(dependencies: {
    state: SimulatorStateRepository;
    orders: PendingKitchenOrdersReader;
    owners: RestaurantOwners;
    commands: SimulatedKitchenCommands;
    clock: Clock;
    random: () => number;
    delay: Readonly<{ minMs: number; maxMs: number }>;
    log: (message: string, fields?: Record<string, unknown>) => void;
  }) {
    this.#state = dependencies.state;
    this.#orders = dependencies.orders;
    this.#owners = dependencies.owners;
    this.#commands = dependencies.commands;
    this.#clock = dependencies.clock;
    this.#random = dependencies.random;
    this.#delay = dependencies.delay;
    this.#log = dependencies.log;
  }

  /** Orders with a scheduled decision or a Preparation plan. */
  get scheduled(): number {
    return this.#schedule.size;
  }

  /** Planned remaining Preparation of an order; `undefined` without a plan. */
  planOf(restaurantOrderId: string): PreparationPlan | undefined {
    const entry = this.#schedule.get(restaurantOrderId);
    return entry?.kind === "preparation" ? entry.plan : undefined;
  }

  async tick(signal?: AbortSignal): Promise<void> {
    if (!(await this.#state.isEnabled())) {
      this.#schedule.clear();
      return;
    }
    const now = this.#clock.now().getTime();
    const pending = await this.#orders.listPendingKitchenOrders();
    const seen = new Set<string>();
    const due: PendingKitchenOrder[] = [];
    const late: Array<{ order: PendingKitchenOrder; readyAt: number }> = [];
    for (const order of pending) {
      seen.add(order.restaurantOrderId);
      const { state } = order;
      const entry = this.#schedule.get(order.restaurantOrderId);
      if (state === RestaurantOrderState.Received) {
        let decision = entry;
        if (decision === undefined || decision.kind !== "decision") {
          decision = {
            kind: "decision",
            dueAt: now + randomDelayMs(this.#delay, this.#random()),
          };
          this.#schedule.set(order.restaurantOrderId, decision);
        }
        if (decision.dueAt <= now) due.push(order);
        continue;
      }
      const step =
        entry?.kind === "preparation"
          ? entry.plan.steps.find((candidate) => candidate.state === state)
          : undefined;
      if (step !== undefined) {
        if (step.dueAt <= now) due.push(order);
        continue;
      }
      // No plan: plan the remaining steps from now.
      const plan = planPreparation(state, now, this.#delay, this.#random);
      this.#schedule.set(order.restaurantOrderId, {
        kind: "preparation",
        plan,
      });
      const readyAt = plannedReadyAt(plan);
      if (
        order.expectedReadyAt === undefined ||
        readyAt > order.expectedReadyAt.getTime()
      ) {
        late.push({ order, readyAt });
      }
    }
    for (const key of this.#schedule.keys()) {
      if (!seen.has(key)) this.#schedule.delete(key);
    }
    for (const { order, readyAt } of late) {
      if (signal?.aborted) return;
      await this.#attempt(order, "report_delay", () =>
        this.#reportDelay(order, readyAt),
      );
    }
    for (const order of due) {
      if (signal?.aborted) return;
      const action = chooseKitchenAction(order.state, this.#random());
      await this.#attempt(order, action, () => this.#act(order, action));
    }
  }

  /** Runs `step`; a failure is logged and drops the order's entry. */
  async #attempt(
    order: PendingKitchenOrder,
    action: KitchenAction,
    step: () => Promise<void>,
  ): Promise<void> {
    try {
      await step();
    } catch (error) {
      this.#schedule.delete(order.restaurantOrderId);
      this.#log("simulated kitchen action failed", {
        restaurant_order_id: order.restaurantOrderId,
        restaurant_id: order.restaurantId,
        market_id: order.marketId,
        action,
        error,
      });
    }
  }

  async #act(order: PendingKitchenOrder, action: KitchenAction): Promise<void> {
    const command = await this.#commandFor(order);
    switch (action) {
      case "accept": {
        this.#schedule.delete(order.restaurantOrderId);
        if (order.scheduled) {
          await this.#commands.accept.execute(command);
          return;
        }
        const plan = planPreparation(
          RestaurantOrderState.Queued,
          this.#clock.now().getTime(),
          this.#delay,
          this.#random,
        );
        await this.#commands.accept.execute({
          ...command,
          expectedReadyAt: new Date(plannedReadyAt(plan)),
        });
        this.#schedule.set(order.restaurantOrderId, {
          kind: "preparation",
          plan,
        });
        return;
      }
      case "reject":
        this.#schedule.delete(order.restaurantOrderId);
        await this.#commands.reject.execute({
          ...command,
          reason: SIMULATED_REJECTION_REASON,
        });
        return;
      case "start_preparation":
        await this.#startPreparation(order, command);
        return;
      case "mark_assembled":
        await this.#commands.markAssembled.execute(command);
        return;
      case "mark_ready":
        await this.#commands.markReady.execute(command);
        return;
      case "report_failure":
        await this.#commands.reportFailure.execute({
          ...command,
          reason: SIMULATED_FAILURE_REASON,
        });
        return;
      case "report_delay": {
        // Later than the planned READY, the current expected time and now.
        const known = [this.#planned(order), this.#clock.now().getTime()];
        if (order.expectedReadyAt !== undefined) {
          known.push(order.expectedReadyAt.getTime());
        }
        const readyAt =
          Math.max(...known) +
          Math.max(1, randomDelayMs(this.#delay, this.#random()));
        await this.#reportDelay(order, readyAt);
        return;
      }
    }
  }

  /**
   * Takes the next order of the Kitchen Queue like the kitchen does (FR-225,
   * FR-655): the queue may start another order than the one whose step is
   * due, e.g. a premium one. Without a free cook (FR-618) the step stays due
   * and is tried again on the next pass. The remaining steps of the started
   * order move by how late its start was; a `READY` later than its
   * `expectedReadyAt` is reported as a delay.
   */
  async #startPreparation(
    due: PendingKitchenOrder,
    command: KitchenOrderCommand,
  ): Promise<void> {
    let started: Awaited<
      ReturnType<SimulatedKitchenCommands["startPreparation"]["execute"]>
    >;
    try {
      started = await this.#commands.startPreparation.execute({
        subject: command.subject,
        restaurantId: command.restaurantId,
        requestId: randomUUID(),
      });
    } catch (error) {
      if (error instanceof NoCookAvailableError) return;
      throw error;
    }
    if (started === undefined) return;
    const order: PendingKitchenOrder = {
      restaurantOrderId: started.restaurantOrderId,
      restaurantId: due.restaurantId,
      marketId: due.marketId,
      state: RestaurantOrderState.Queued,
      scheduled: started.schedule !== undefined,
      ...(started.timeline.expectedReadyAt === undefined
        ? {}
        : { expectedReadyAt: started.timeline.expectedReadyAt }),
    };
    const plan = this.planOf(order.restaurantOrderId);
    const start = plan?.steps.find(
      (step) => step.state === RestaurantOrderState.Queued,
    );
    if (plan === undefined || start === undefined) {
      throw new Error(
        `restaurant order ${order.restaurantOrderId} has no preparation plan`,
      );
    }
    const late = Math.max(0, this.#clock.now().getTime() - start.dueAt);
    const remaining: PreparationPlan = {
      steps: plan.steps
        .filter((step) => step.state !== RestaurantOrderState.Queued)
        .map((step) => ({ ...step, dueAt: step.dueAt + late })),
    };
    this.#schedule.set(order.restaurantOrderId, {
      kind: "preparation",
      plan: remaining,
    });
    const readyAt = plannedReadyAt(remaining);
    if (
      order.expectedReadyAt === undefined ||
      readyAt > order.expectedReadyAt.getTime()
    ) {
      await this.#reportDelay(order, readyAt);
    }
  }

  /**
   * Reports `readyAt` as the new `expectedReadyAt` and shifts the order's
   * plan so `READY` lands at it.
   */
  async #reportDelay(
    order: PendingKitchenOrder,
    readyAt: number,
  ): Promise<void> {
    const entry = this.#schedule.get(order.restaurantOrderId);
    if (entry === undefined || entry.kind !== "preparation") {
      throw new Error(
        `restaurant order ${order.restaurantOrderId} has no preparation plan`,
      );
    }
    await this.#commands.reportDelay.execute({
      ...(await this.#commandFor(order)),
      expectedReadyAt: new Date(readyAt),
      reason: SIMULATED_DELAY_REASON,
    });
    this.#schedule.set(order.restaurantOrderId, {
      kind: "preparation",
      plan: shiftPlan(entry.plan, readyAt),
    });
  }

  /** Planned `READY` moment of an order with a plan. */
  #planned(order: PendingKitchenOrder): number {
    const plan = this.planOf(order.restaurantOrderId);
    if (plan === undefined) {
      throw new Error(
        `restaurant order ${order.restaurantOrderId} has no preparation plan`,
      );
    }
    return plannedReadyAt(plan);
  }

  async #commandFor(order: PendingKitchenOrder): Promise<KitchenOrderCommand> {
    const ownerId = await this.#owners.ownerOf(order.restaurantId);
    if (ownerId === undefined) {
      throw new Error(`restaurant ${order.restaurantId} has no owner`);
    }
    return {
      subject: { type: "user", id: ownerId },
      restaurantId: order.restaurantId,
      restaurantOrderId: order.restaurantOrderId,
    };
  }
}
