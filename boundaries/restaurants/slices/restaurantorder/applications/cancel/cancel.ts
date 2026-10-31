import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { Outbox } from "../../../../pkg/outbox/outbox.ts";
import type { UnitOfWork } from "../../../../pkg/unit-of-work/unit-of-work.ts";
import type { Clock } from "../../../../pkg/usecase/clock.ts";
import { NotFoundError } from "../../../../pkg/usecase/errors.ts";
import type {
  CancellationDecision,
  CancellationPolicy,
  CancellationRejectionReason,
  RestaurantOrderRepository,
  RestaurantOrderState,
} from "../../domains/restaurantorder/restaurantorder.ts";
import type { FulfillmentCoordinatorAccess } from "../access-control/access-control.ts";
import { saveAndPublish } from "../publish-change/publish-change.ts";

export type CancelRestaurantOrder = Readonly<{
  /** `service` identity of the `orders` fulfillment process. */
  subject: Subject;
  restaurantId: string;
  orderId: string;
  reason?: string;
}>;

export { CancellationRejectionReason } from "../../domains/restaurantorder/restaurantorder.ts";

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

const toResult = (
  restaurantOrderId: string,
  decision: CancellationDecision,
): CancelRestaurantOrderResult =>
  decision.outcome === "rejected"
    ? {
        outcome: "cancellation_rejected",
        restaurantOrderId,
        reason: decision.reason,
        state: decision.state,
      }
    : {
        outcome: "cancelled",
        restaurantOrderId,
        stage: decision.stage,
        repeated: decision.outcome === "repeated",
      };

/**
 * Compensation command of `orders`. The Restaurant Order decides the outcome
 * by stage; the handler persists it and publishes `RestaurantOrderCancelled`
 * only when the State changes. Cancellation during Preparation follows the
 * explicit policy. An unknown `orderId` is not found: no tombstone is kept
 * for Cancel before Receive.
 */
export class CancelRestaurantOrderHandler {
  readonly #accessControl: FulfillmentCoordinatorAccess;
  readonly #unitOfWork: UnitOfWork;
  readonly #restaurantOrders: RestaurantOrderRepository;
  readonly #outbox: Outbox;
  readonly #clock: Clock;
  readonly #policy: CancellationPolicy;

  constructor(dependencies: {
    accessControl: FulfillmentCoordinatorAccess;
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
    await requirePermission(
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
      const decision = order.cancel({
        cancelledAt: now,
        policy: this.#policy,
        reason: command.reason,
      });
      if (decision.outcome === "cancelled") {
        await saveAndPublish(this.#restaurantOrders, this.#outbox, order, [
          "RestaurantOrderCancelled",
        ]);
      }
      return toResult(order.id, decision);
    });
  }
}
