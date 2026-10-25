import {
  InvalidRestaurantOrderError,
  InvalidRestaurantOrderReason,
} from "./errors.ts";
import {
  type AcceptanceEligibility,
  type CancellationPolicy,
  RestaurantOrderState,
} from "./fsm.ts";
import {
  compositionKey,
  optionalText,
  type PreparationItem,
  type PreparationItemInput,
  toPreparationItems,
} from "./items.ts";
import {
  RestaurantOrderRecord,
  type RestaurantOrderSnapshot,
} from "./record.ts";
import { Quantity } from "./vo/quantity.ts";

export {
  DuplicateOrderIdError,
  InvalidRestaurantOrderError,
  InvalidRestaurantOrderReason,
} from "./errors.ts";
export type * from "./events.ts";
export {
  type AcceptanceEligibility,
  AlreadyInStateError,
  type CancellationPolicy,
  GuardFailedError,
  InvalidTransitionError,
  RestaurantOrderState,
} from "./fsm.ts";
export type { PreparationItem, PreparationItemInput } from "./items.ts";
export type {
  RestaurantOrderSnapshot,
  RestaurantOrderTimeline,
} from "./record.ts";

/**
 * Aggregate root of the kitchen lifecycle of one `orders.Order` inside
 * `restaurants`. `Kitchen Order` is its role after Acceptance, not a separate
 * aggregate. Every state change goes through `resolveTransition`; domain
 * methods record Domain Events explicitly. Fields and the snapshot live in
 * {@link RestaurantOrderRecord}.
 */
export class RestaurantOrder extends RestaurantOrderRecord {
  /** Creates the order in `RECEIVED` from the composition sent by `orders`. */
  static receive(input: {
    id: string;
    orderId: string;
    restaurantId: string;
    items: readonly PreparationItemInput[];
    receivedAt: Date;
  }): RestaurantOrder {
    const order = new RestaurantOrder(
      {
        id: input.id,
        orderId: input.orderId,
        restaurantId: input.restaurantId,
        state: RestaurantOrderState.Received,
        timeline: {},
      },
      toPreparationItems(input.items),
      0,
    );
    order.record({ type: "RestaurantOrderReceived" }, input.receivedAt);
    return order;
  }

  static restore(
    snapshot: RestaurantOrderSnapshot,
    version: number,
  ): RestaurantOrder {
    return new RestaurantOrder(
      snapshot,
      snapshot.items.map((item) => ({
        menuItemId: item.menuItemId,
        quantity: Quantity.of(item.quantity),
        options: [...item.options],
      })),
      version,
    );
  }

  /** Whether a repeated Receive Order Request carries the same request. */
  matchesRequest(input: {
    restaurantId: string;
    items: readonly PreparationItemInput[];
  }): boolean {
    if (input.restaurantId !== this.restaurantId) {
      return false;
    }
    let items: PreparationItem[];
    try {
      items = toPreparationItems(input.items);
    } catch {
      return false;
    }
    return compositionKey(items) === compositionKey(this.items);
  }

  accept(input: {
    acceptedAt: Date;
    expectedReadyAt: Date;
    eligibility: AcceptanceEligibility;
  }): void {
    this.apply({ trigger: "Accept", eligibility: input.eligibility });
    this.extendTimeline({
      acceptedAt: input.acceptedAt,
      expectedReadyAt: input.expectedReadyAt,
    });
    this.record(
      {
        type: "RestaurantOrderAccepted",
        acceptedAt: input.acceptedAt,
        expectedReadyAt: input.expectedReadyAt,
      },
      input.acceptedAt,
    );
  }

  reject(input: { rejectedAt: Date; reason?: string }): void {
    const stage = this.state;
    this.apply({ trigger: "Reject" });
    this.setRejectionReason(optionalText(input.reason));
    this.record(
      {
        type: "RestaurantOrderRejected",
        stage,
        reason: this.rejectionReason,
      },
      input.rejectedAt,
    );
  }

  queue(input: { queuedAt: Date }): void {
    this.apply({ trigger: "Queue" });
    this.extendTimeline({ queuedAt: input.queuedAt });
    this.record(
      { type: "RestaurantOrderQueued", queuedAt: input.queuedAt },
      input.queuedAt,
    );
  }

  startPreparation(input: { startedAt: Date }): void {
    this.apply({ trigger: "StartPreparation" });
    this.extendTimeline({ preparationStartedAt: input.startedAt });
    this.record(
      {
        type: "PreparationStarted",
        preparationStartedAt: input.startedAt,
        expectedReadyAt: this.timeline.expectedReadyAt,
      },
      input.startedAt,
    );
  }

  /**
   * Kitchen confirms that every required item is prepared and assembled.
   * Without item-level model the command itself is that statement.
   */
  markAssembled(input: { assembledAt: Date }): void {
    this.apply({ trigger: "MarkAssembled" });
    this.record({ type: "RestaurantOrderAssembled" }, input.assembledAt);
  }

  markReady(input: { readyAt: Date }): void {
    this.apply({ trigger: "MarkReady" });
    this.extendTimeline({ readyAt: input.readyAt });
    this.record(
      { type: "RestaurantOrderReady", readyAt: input.readyAt },
      input.readyAt,
    );
  }

  reportDelay(input: {
    reportedAt: Date;
    expectedReadyAt: Date;
    reason?: string;
  }): void {
    const previousExpectedReadyAt = this.timeline.expectedReadyAt;
    this.apply({
      trigger: "ReportDelay",
      now: input.reportedAt,
      currentExpectedReadyAt: previousExpectedReadyAt,
      requestedExpectedReadyAt: input.expectedReadyAt,
    });
    this.extendTimeline({ expectedReadyAt: input.expectedReadyAt });
    this.record(
      {
        type: "PreparationDelayed",
        expectedReadyAt: input.expectedReadyAt,
        previousExpectedReadyAt,
        reason: optionalText(input.reason),
      },
      input.reportedAt,
    );
  }

  reportFailure(input: { failedAt: Date; reason: string }): void {
    const reason = optionalText(input.reason);
    if (reason === undefined) {
      throw new InvalidRestaurantOrderError(
        InvalidRestaurantOrderReason.EmptyFailureReason,
      );
    }
    const stage = this.state;
    this.apply({ trigger: "ReportFailure" });
    this.setFailureReason(reason);
    this.record(
      { type: "RestaurantOrderFailed", stage, reason },
      input.failedAt,
    );
  }

  cancel(input: {
    cancelledAt: Date;
    policy: CancellationPolicy;
    reason?: string;
  }): void {
    const stage = this.state;
    this.apply({ trigger: "Cancel", policy: input.policy });
    const reason = optionalText(input.reason);
    this.setCancellation({ stage, reason });
    this.record(
      {
        type: "RestaurantOrderCancelled",
        stage,
        preparationStarted: this.timeline.preparationStartedAt !== undefined,
        reason,
      },
      input.cancelledAt,
    );
  }
}

/**
 * Repository contract of {@link RestaurantOrder}. `orderId` is unique: saving a
 * new order for a known `orderId` throws {@link DuplicateOrderIdError}. `save`
 * uses optimistic concurrency and returns the committed version.
 */
export interface RestaurantOrderRepository {
  get(id: string): Promise<RestaurantOrder | undefined>;
  findByOrderId(orderId: string): Promise<RestaurantOrder | undefined>;
  save(order: RestaurantOrder): Promise<number>;
}
