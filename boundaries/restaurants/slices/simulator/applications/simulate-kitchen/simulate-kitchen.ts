import type { Clock } from "../../../../pkg/application/clock.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import type { AcceptOrderHandler } from "../../../restaurantorder/applications/accept-order/accept-order.ts";
import type { MarkAssembledHandler } from "../../../restaurantorder/applications/mark-assembled/mark-assembled.ts";
import type { MarkReadyForPickupHandler } from "../../../restaurantorder/applications/mark-ready-for-pickup/mark-ready-for-pickup.ts";
import type { RejectOrderHandler } from "../../../restaurantorder/applications/reject-order/reject-order.ts";
import type { ReportPreparationDelayHandler } from "../../../restaurantorder/applications/report-preparation-delay/report-preparation-delay.ts";
import type { ReportPreparationFailureHandler } from "../../../restaurantorder/applications/report-preparation-failure/report-preparation-failure.ts";
import type { StartPreparationHandler } from "../../../restaurantorder/applications/start-preparation/start-preparation.ts";
import {
  chooseKitchenAction,
  type KitchenAction,
  randomDelayMs,
  type SimulatedState,
  type SimulatorStateRepository,
} from "../../domains/simulator/simulator.ts";

/** Restaurant Order waiting for an action of its restaurant. */
export type PendingKitchenOrder = Readonly<{
  restaurantOrderId: string;
  restaurantId: string;
  state: SimulatedState;
  expectedReadyAt?: Date;
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

type ScheduledAction = { readonly dueAt: number };

/**
 * One pass of the kitchen Simulator (FR-605). Disabled: forgets the schedule
 * and does nothing (AC-764). Enabled: every pending Restaurant Order gets a
 * due time `firstSeen + random [minDelay, maxDelay]` per state; when due, one
 * weighted action is performed through the real use case on behalf of the
 * restaurant owner, so the normal `operate_kitchen` check applies. A failed
 * action is logged and dropped; the pass continues.
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
  /** Keyed by `restaurantOrderId:state`; in-process only. */
  readonly #schedule = new Map<string, ScheduledAction>();

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

  /** Orders waiting for their due time. */
  get scheduled(): number {
    return this.#schedule.size;
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
    for (const order of pending) {
      const key = `${order.restaurantOrderId}:${order.state}`;
      seen.add(key);
      let entry = this.#schedule.get(key);
      if (entry === undefined) {
        entry = { dueAt: now + randomDelayMs(this.#delay, this.#random()) };
        this.#schedule.set(key, entry);
      }
      if (entry.dueAt <= now) due.push(order);
    }
    for (const key of this.#schedule.keys()) {
      if (!seen.has(key)) this.#schedule.delete(key);
    }
    for (const order of due) {
      if (signal?.aborted) return;
      this.#schedule.delete(`${order.restaurantOrderId}:${order.state}`);
      const action = chooseKitchenAction(order.state, this.#random());
      try {
        await this.#perform(order, action);
      } catch (error) {
        this.#log("simulated kitchen action failed", {
          restaurant_order_id: order.restaurantOrderId,
          restaurant_id: order.restaurantId,
          action,
          error,
        });
      }
    }
  }

  async #perform(
    order: PendingKitchenOrder,
    action: KitchenAction,
  ): Promise<void> {
    const ownerId = await this.#owners.ownerOf(order.restaurantId);
    if (ownerId === undefined) {
      throw new Error(`restaurant ${order.restaurantId} has no owner`);
    }
    const subject: Subject = { type: "user", id: ownerId };
    const command = {
      subject,
      restaurantId: order.restaurantId,
      restaurantOrderId: order.restaurantOrderId,
    };
    switch (action) {
      case "accept":
        await this.#commands.accept.execute(command);
        return;
      case "reject":
        await this.#commands.reject.execute({
          ...command,
          reason: SIMULATED_REJECTION_REASON,
        });
        return;
      case "start_preparation":
        await this.#commands.startPreparation.execute(command);
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
        // Later than both the current expected time and now.
        const now = this.#clock.now().getTime();
        const base = Math.max(order.expectedReadyAt?.getTime() ?? now, now);
        await this.#commands.reportDelay.execute({
          ...command,
          expectedReadyAt: new Date(
            base + Math.max(1, randomDelayMs(this.#delay, this.#random())),
          ),
          reason: SIMULATED_DELAY_REASON,
        });
        return;
      }
    }
  }
}
