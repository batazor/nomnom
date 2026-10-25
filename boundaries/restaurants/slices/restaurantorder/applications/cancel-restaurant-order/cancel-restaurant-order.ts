import type { Clock } from "../../../../pkg/application/clock.ts";
import { NotFoundError } from "../../../../pkg/application/errors.ts";
import type { Outbox } from "../../../../pkg/application/outbox.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import type { UnitOfWork } from "../../../../pkg/application/unit-of-work.ts";
import {
  AlreadyInStateError,
  type CancellationPolicy,
  GuardFailedError,
  InvalidTransitionError,
  type RestaurantOrderRepository,
  RestaurantOrderState,
} from "../../domains/restaurantorder/restaurantorder.ts";
import type { KitchenAccessControl } from "../access-control.ts";
import { authorize, saveAndPublish } from "../restaurant-order-support.ts";

export type CancelRestaurantOrder = Readonly<{
  /** `service` identity of the `orders` fulfillment process. */
  subject: Subject;
  restaurantId: string;
  orderId: string;
  reason?: string;
}>;

export const CancellationRejectionReason = {
  AlreadyReady: "already_ready",
  AlreadyRejected: "already_rejected",
  AlreadyFailed: "already_failed",
  CancellationDuringPreparationNotAllowed:
    "cancellation_during_preparation_not_allowed",
} as const;

export type CancellationRejectionReason =
  (typeof CancellationRejectionReason)[keyof typeof CancellationRejectionReason];

/** Business result by stage; a rejection is not a technical error. */
export type CancelRestaurantOrderResult =
  | Readonly<{
      outcome: "cancelled";
      restaurantOrderId: string;
      /** State at the moment of cancellation. */
      stage: RestaurantOrderState;
      /** `true` when the order had already been cancelled before this call. */
      repeated: boolean;
    }>
  | Readonly<{
      outcome: "cancellation_rejected";
      restaurantOrderId: string;
      reason: CancellationRejectionReason;
      state: RestaurantOrderState;
    }>;

const TERMINAL_REJECTIONS: Partial<
  Record<RestaurantOrderState, CancellationRejectionReason>
> = {
  [RestaurantOrderState.Ready]: CancellationRejectionReason.AlreadyReady,
  [RestaurantOrderState.Rejected]: CancellationRejectionReason.AlreadyRejected,
  [RestaurantOrderState.Failed]: CancellationRejectionReason.AlreadyFailed,
};

/**
 * Compensation command of `orders`. Cancels by stage and answers with a
 * business result; publishes `RestaurantOrderCancelled` only when the State
 * changes. Cancellation during Preparation follows the explicit policy. An
 * unknown `orderId` is not found: no tombstone is kept for Cancel before
 * Receive.
 */
export class CancelRestaurantOrderHandler {
  readonly #accessControl: KitchenAccessControl;
  readonly #unitOfWork: UnitOfWork;
  readonly #restaurantOrders: RestaurantOrderRepository;
  readonly #outbox: Outbox;
  readonly #clock: Clock;
  readonly #policy: CancellationPolicy;

  constructor(dependencies: {
    accessControl: KitchenAccessControl;
    unitOfWork: UnitOfWork;
    restaurantOrders: RestaurantOrderRepository;
    outbox: Outbox;
    clock: Clock;
    cancellationDuringPreparation: CancellationPolicy["duringPreparation"];
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#unitOfWork = dependencies.unitOfWork;
    this.#restaurantOrders = dependencies.restaurantOrders;
    this.#outbox = dependencies.outbox;
    this.#clock = dependencies.clock;
    this.#policy = {
      duringPreparation: dependencies.cancellationDuringPreparation,
    };
  }

  async execute(
    command: CancelRestaurantOrder,
  ): Promise<CancelRestaurantOrderResult> {
    await authorize(
      this.#accessControl.canCoordinateFulfillment(
        command.subject,
        command.restaurantId,
      ),
      "cancel restaurant order",
    );
    const now = this.#clock.now();
    return this.#unitOfWork.run(async () => {
      const order = await this.#restaurantOrders.findByOrderId(command.orderId);
      if (order === undefined || order.restaurantId !== command.restaurantId) {
        throw new NotFoundError("RestaurantOrder", command.orderId);
      }
      const stage = order.state;
      try {
        order.cancel({
          cancelledAt: now,
          policy: this.#policy,
          reason: command.reason,
        });
      } catch (error) {
        if (error instanceof AlreadyInStateError) {
          return {
            outcome: "cancelled",
            restaurantOrderId: order.id,
            stage: order.cancelledAtStage ?? stage,
            repeated: true,
          };
        }
        if (error instanceof GuardFailedError) {
          return {
            outcome: "cancellation_rejected",
            restaurantOrderId: order.id,
            reason:
              CancellationRejectionReason.CancellationDuringPreparationNotAllowed,
            state: stage,
          };
        }
        const terminal = TERMINAL_REJECTIONS[stage];
        if (error instanceof InvalidTransitionError && terminal !== undefined) {
          return {
            outcome: "cancellation_rejected",
            restaurantOrderId: order.id,
            reason: terminal,
            state: stage,
          };
        }
        throw error;
      }
      await saveAndPublish(this.#restaurantOrders, this.#outbox, order, [
        "RestaurantOrderCancelled",
      ]);
      return {
        outcome: "cancelled",
        restaurantOrderId: order.id,
        stage,
        repeated: false,
      };
    });
  }
}
