import {
  type CancellationDecision,
  CancellationRejectionReason,
  TERMINAL_CANCELLATION_REJECTIONS,
} from "./cancellation.ts";
import {
  InvalidRestaurantOrderError,
  InvalidRestaurantOrderReason,
} from "./errors.ts";
import type { EventPayload, RestaurantOrderEvent } from "./events.ts";
import {
  type AcceptanceEligibility,
  AlreadyInStateError,
  type CancellationPolicy,
  GuardFailedError,
  InvalidTransitionError,
  RestaurantOrderState,
  resolveTransition,
  type TransitionRequest,
} from "./fsm.ts";
import {
  compositionKey,
  optionalText,
  type PreparationItem,
  type PreparationItemInput,
  toPreparationItems,
} from "./items.ts";
import { Quantity } from "./vo/quantity.ts";

export {
  type CancellationDecision,
  CancellationRejectionReason,
} from "./cancellation.ts";
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
  isIn,
  leavesOf,
  RestaurantOrderCompoundState,
  RestaurantOrderState,
} from "./fsm.ts";
export type { PreparationItem, PreparationItemInput } from "./items.ts";

export type RestaurantOrderTimeline = Readonly<{
  /** Unset on orders received before the timeline recorded it. */
  receivedAt?: Date;
  acceptedAt?: Date;
  queuedAt?: Date;
  preparationStartedAt?: Date;
  expectedReadyAt?: Date;
  readyAt?: Date;
}>;

export type RestaurantOrderSnapshot = Readonly<{
  id: string;
  orderId: string;
  restaurantId: string;
  items: readonly Readonly<{
    menuItemId: string;
    quantity: number;
    options: readonly string[];
  }>[];
  state: RestaurantOrderState;
  timeline: RestaurantOrderTimeline;
  rejectionReason?: string;
  failureReason?: string;
  cancellation?: Readonly<{ stage: RestaurantOrderState; reason?: string }>;
}>;

/**
 * Aggregate root of the kitchen lifecycle of one `orders.Order` inside
 * `restaurants`. `Kitchen Order` is its role after Acceptance, not a separate
 * aggregate. Every state change goes through `resolveTransition`; domain
 * methods record Domain Events explicitly.
 */
export class RestaurantOrder {
  readonly #id: string;
  readonly #orderId: string;
  readonly #restaurantId: string;
  readonly #items: readonly PreparationItem[];
  readonly #version: number;
  #state: RestaurantOrderState;
  #timeline: RestaurantOrderTimeline;
  #rejectionReason: string | undefined;
  #failureReason: string | undefined;
  #cancellation: RestaurantOrderSnapshot["cancellation"];
  #events: RestaurantOrderEvent[] = [];

  private constructor(
    snapshot: Omit<RestaurantOrderSnapshot, "items">,
    items: readonly PreparationItem[],
    version: number,
  ) {
    this.#id = snapshot.id;
    this.#orderId = snapshot.orderId;
    this.#restaurantId = snapshot.restaurantId;
    this.#items = items;
    this.#state = snapshot.state;
    this.#timeline = { ...snapshot.timeline };
    this.#rejectionReason = snapshot.rejectionReason;
    this.#failureReason = snapshot.failureReason;
    this.#cancellation = snapshot.cancellation;
    this.#version = version;
  }

  get id(): string {
    return this.#id;
  }

  get orderId(): string {
    return this.#orderId;
  }

  get restaurantId(): string {
    return this.#restaurantId;
  }

  get items(): readonly PreparationItem[] {
    return this.#items;
  }

  get state(): RestaurantOrderState {
    return this.#state;
  }

  get timeline(): RestaurantOrderTimeline {
    return this.#timeline;
  }

  get rejectionReason(): string | undefined {
    return this.#rejectionReason;
  }

  get failureReason(): string | undefined {
    return this.#failureReason;
  }

  /** State in which the order was cancelled, if it was. */
  get cancelledAtStage(): RestaurantOrderState | undefined {
    return this.#cancellation?.stage;
  }

  get version(): number {
    return this.#version;
  }

  toSnapshot(): RestaurantOrderSnapshot {
    return {
      id: this.#id,
      orderId: this.#orderId,
      restaurantId: this.#restaurantId,
      items: this.#items.map((item) => ({
        menuItemId: item.menuItemId,
        quantity: item.quantity.value,
        options: [...item.options],
      })),
      state: this.#state,
      timeline: { ...this.#timeline },
      ...(this.#rejectionReason === undefined
        ? {}
        : { rejectionReason: this.#rejectionReason }),
      ...(this.#failureReason === undefined
        ? {}
        : { failureReason: this.#failureReason }),
      ...(this.#cancellation === undefined
        ? {}
        : { cancellation: this.#cancellation }),
    };
  }

  pullEvents(): RestaurantOrderEvent[] {
    const events = this.#events;
    this.#events = [];
    return events;
  }

  private apply(request: TransitionRequest): void {
    const outcome = resolveTransition(this.#state, request);
    if (!outcome.ok) {
      throw outcome.error;
    }
    this.#state = outcome.to;
  }

  private extendTimeline(timeline: RestaurantOrderTimeline): void {
    this.#timeline = { ...this.#timeline, ...timeline };
  }

  private setRejectionReason(reason: string | undefined): void {
    this.#rejectionReason = reason;
  }

  private setFailureReason(reason: string): void {
    this.#failureReason = reason;
  }

  private setCancellation(
    cancellation: NonNullable<RestaurantOrderSnapshot["cancellation"]>,
  ): void {
    this.#cancellation = cancellation;
  }

  private record(
    payload: EventPayload<RestaurantOrderEvent>,
    occurredAt: Date,
  ): void {
    this.#events.push({
      ...payload,
      restaurantOrderId: this.#id,
      orderId: this.#orderId,
      restaurantId: this.#restaurantId,
      occurredAt,
    } as RestaurantOrderEvent);
  }

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
        timeline: { receivedAt: input.receivedAt },
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

  /**
   * Accepts with the expected ready time named by the kitchen or estimated;
   * it MUST be later than the acceptance moment (FR-609).
   */
  accept(input: {
    acceptedAt: Date;
    expectedReadyAt: Date;
    eligibility: AcceptanceEligibility;
  }): void {
    if (input.expectedReadyAt.getTime() <= input.acceptedAt.getTime()) {
      throw new InvalidRestaurantOrderError(
        InvalidRestaurantOrderReason.ExpectedReadyAtNotLater,
      );
    }
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

  /**
   * Cancels by stage and returns the decision: a repeat answers with the
   * stage of the first cancellation, Preparation follows `policy` and a
   * terminal State is rejected with its reason. Only `cancelled` changes the
   * State and records `RestaurantOrderCancelled`.
   */
  cancel(input: {
    cancelledAt: Date;
    policy: CancellationPolicy;
    reason?: string;
  }): CancellationDecision {
    const stage = this.state;
    try {
      this.apply({ trigger: "Cancel", policy: input.policy });
    } catch (error) {
      if (error instanceof AlreadyInStateError) {
        return { outcome: "repeated", stage: this.cancelledAtStage ?? stage };
      }
      if (error instanceof GuardFailedError) {
        return {
          outcome: "rejected",
          reason:
            CancellationRejectionReason.CancellationDuringPreparationNotAllowed,
          state: stage,
        };
      }
      const terminal = TERMINAL_CANCELLATION_REJECTIONS[stage];
      if (error instanceof InvalidTransitionError && terminal !== undefined) {
        return { outcome: "rejected", reason: terminal, state: stage };
      }
      throw error;
    }
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
    return { outcome: "cancelled", stage };
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
