import { MarketId } from "../../../restaurant/domains/restaurant/vo/market-id.ts";
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
import { OrderSchedule, type OrderScheduleValue } from "./vo/order-schedule.ts";
import { Quantity } from "./vo/quantity.ts";

export {
  type CancellationDecision,
  CancellationRejectionReason,
} from "./cancellation.ts";
export {
  DuplicateOrderIdError,
  InvalidRestaurantOrderError,
  InvalidRestaurantOrderReason,
  MarketMismatchError,
  NoCookAvailableError,
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
export {
  InvalidOrderScheduleError,
  InvalidOrderScheduleReason,
  OrderSchedule,
  type OrderScheduleValue,
} from "./vo/order-schedule.ts";

/** Schedule block of a Receive Order Request; every field is required. */
export type OrderScheduleInput = Readonly<{
  windowStart?: Date;
  windowEnd?: Date;
  plannedReadyAt?: Date;
}>;

/** Premium Priority chosen at Place Order (FR-651); never changes. */
export const OrderPriority = {
  Standard: "STANDARD",
  Premium: "PREMIUM",
} as const;

export type OrderPriority = (typeof OrderPriority)[keyof typeof OrderPriority];

const orderPriority = (value: string): OrderPriority => {
  if (value !== OrderPriority.Standard && value !== OrderPriority.Premium) {
    throw new InvalidRestaurantOrderError(
      InvalidRestaurantOrderReason.UnknownPriority,
    );
  }
  return value;
};

export type RestaurantOrderTimeline = Readonly<{
  /** Unset on orders received before the timeline recorded it. */
  receivedAt?: Date;
  acceptedAt?: Date;
  queuedAt?: Date;
  preparationStartedAt?: Date;
  expectedReadyAt?: Date;
  readyAt?: Date;
  /** Since schemaVersion 4: the courier took the order (Pickup). */
  pickedUpAt?: Date;
}>;

export type RestaurantOrderSnapshot = Readonly<{
  id: string;
  orderId: string;
  restaurantId: string;
  /** Market of the restaurant that received the order (ADR 41), immutable. */
  marketId: string;
  items: readonly Readonly<{
    menuItemId: string;
    quantity: number;
    options: readonly string[];
  }>[];
  state: RestaurantOrderState;
  timeline: RestaurantOrderTimeline;
  priority: OrderPriority;
  /**
   * Delivery Window and Planned Ready At of a scheduled order (FR-683);
   * absent for an order without a window.
   */
  schedule?: OrderScheduleValue;
  /** Cook assigned by Start Preparation; kept after the order is closed. */
  cookId?: string;
  /** `request_id` of the Start Next Preparation that started the order. */
  startRequestId?: string;
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
  readonly #marketId: MarketId;
  readonly #items: readonly PreparationItem[];
  readonly #version: number;
  readonly #priority: OrderPriority;
  readonly #schedule: OrderSchedule | undefined;
  #state: RestaurantOrderState;
  #timeline: RestaurantOrderTimeline;
  #cookId: string | undefined;
  #startRequestId: string | undefined;
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
    this.#marketId = MarketId.of(snapshot.marketId);
    this.#items = items;
    this.#state = snapshot.state;
    this.#timeline = { ...snapshot.timeline };
    this.#priority = orderPriority(snapshot.priority);
    this.#schedule =
      snapshot.schedule === undefined
        ? undefined
        : OrderSchedule.of(snapshot.schedule);
    this.#cookId = snapshot.cookId;
    this.#startRequestId = snapshot.startRequestId;
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

  get marketId(): MarketId {
    return this.#marketId;
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

  get priority(): OrderPriority {
    return this.#priority;
  }

  /** Schedule of a scheduled order; `undefined` for an order without a window. */
  get schedule(): OrderSchedule | undefined {
    return this.#schedule;
  }

  /** Cook assigned by Start Preparation, if it happened. */
  get cookId(): string | undefined {
    return this.#cookId;
  }

  /** `request_id` of the Start Next Preparation that started the order. */
  get startRequestId(): string | undefined {
    return this.#startRequestId;
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
      marketId: this.#marketId.value,
      items: this.#items.map((item) => ({
        menuItemId: item.menuItemId,
        quantity: item.quantity.value,
        options: [...item.options],
      })),
      state: this.#state,
      timeline: { ...this.#timeline },
      priority: this.#priority,
      ...(this.#schedule === undefined
        ? {}
        : { schedule: this.#schedule.toValue() }),
      ...(this.#cookId === undefined ? {} : { cookId: this.#cookId }),
      ...(this.#startRequestId === undefined
        ? {}
        : { startRequestId: this.#startRequestId }),
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

  /** Domain Events recorded since the last {@link pullEvents}, not removed. */
  get recordedEvents(): readonly RestaurantOrderEvent[] {
    return [...this.#events];
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
      marketId: this.#marketId.value,
      occurredAt,
    } as RestaurantOrderEvent);
  }

  /**
   * Creates the order in `RECEIVED` from the composition sent by `orders`, in
   * the Market of the restaurant that received it (ADR 41). A scheduled order
   * carries its complete schedule (FR-683).
   */
  static receive(input: {
    id: string;
    orderId: string;
    restaurantId: string;
    marketId: string;
    items: readonly PreparationItemInput[];
    priority: string;
    schedule?: OrderScheduleInput;
    receivedAt: Date;
  }): RestaurantOrder {
    const order = new RestaurantOrder(
      {
        id: input.id,
        orderId: input.orderId,
        restaurantId: input.restaurantId,
        marketId: input.marketId,
        priority: orderPriority(input.priority),
        ...(input.schedule === undefined
          ? {}
          : { schedule: OrderSchedule.of(input.schedule).toValue() }),
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
    priority: string;
    schedule?: OrderScheduleInput;
  }): boolean {
    if (
      input.restaurantId !== this.restaurantId ||
      input.priority !== this.#priority
    ) {
      return false;
    }
    if ((input.schedule === undefined) !== (this.#schedule === undefined)) {
      return false;
    }
    if (input.schedule !== undefined && this.#schedule !== undefined) {
      let schedule: OrderSchedule;
      try {
        schedule = OrderSchedule.of(input.schedule);
      } catch {
        return false;
      }
      if (!schedule.equals(this.#schedule)) return false;
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
   * it MUST be later than the acceptance moment (FR-609). A scheduled order
   * is ready at its Planned Ready At: a time named by the kitchen is invalid
   * (FR-222), and it stays `ACCEPTED` until Queue Restaurant Order.
   */
  accept(input: {
    acceptedAt: Date;
    /** Required without a Delivery Window, forbidden with one. */
    expectedReadyAt: Date | undefined;
    eligibility: AcceptanceEligibility;
  }): void {
    const expectedReadyAt = this.#acceptedReadyAt(input.expectedReadyAt);
    if (expectedReadyAt.getTime() <= input.acceptedAt.getTime()) {
      throw new InvalidRestaurantOrderError(
        InvalidRestaurantOrderReason.ExpectedReadyAtNotLater,
      );
    }
    this.apply({ trigger: "Accept", eligibility: input.eligibility });
    this.extendTimeline({ acceptedAt: input.acceptedAt, expectedReadyAt });
    this.record(
      {
        type: "RestaurantOrderAccepted",
        acceptedAt: input.acceptedAt,
        expectedReadyAt,
      },
      input.acceptedAt,
    );
  }

  #acceptedReadyAt(named: Date | undefined): Date {
    if (this.#schedule !== undefined) {
      if (named !== undefined) {
        throw new InvalidRestaurantOrderError(
          InvalidRestaurantOrderReason.ExpectedReadyAtOfScheduledOrder,
        );
      }
      return this.#schedule.plannedReadyAt;
    }
    if (named === undefined) {
      throw new InvalidRestaurantOrderError(
        InvalidRestaurantOrderReason.MissingExpectedReadyAt,
      );
    }
    return named;
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

  /**
   * Moves an `ACCEPTED` order to the Kitchen Queue; `expected_ready_at` does
   * not change (FR-224). Queued in the Unit of Work of its Acceptance, the
   * order's RestaurantOrderAccepted carries the queuing moment: queuing
   * itself publishes no fact.
   */
  queue(input: { queuedAt: Date }): void {
    this.apply({ trigger: "Queue" });
    this.extendTimeline({ queuedAt: input.queuedAt });
    this.#events = this.#events.map((event) =>
      event.type === "RestaurantOrderAccepted"
        ? { ...event, queuedAt: input.queuedAt }
        : event,
    );
    this.record(
      { type: "RestaurantOrderQueued", queuedAt: input.queuedAt },
      input.queuedAt,
    );
  }

  /**
   * Throws what {@link startPreparation} would throw in the current State,
   * without changing the order: a cook is assigned only to an order that can
   * start.
   */
  checkStartPreparation(): void {
    const outcome = resolveTransition(this.#state, {
      trigger: "StartPreparation",
    });
    if (!outcome.ok) {
      throw outcome.error;
    }
  }

  /**
   * Starts Preparation by the cook assigned from the Cook Pool on the Start
   * Next Preparation `requestId` that chose the order.
   */
  startPreparation(input: {
    startedAt: Date;
    cookId: string;
    requestId: string;
  }): void {
    const cookId = optionalText(input.cookId);
    if (cookId === undefined) {
      throw new InvalidRestaurantOrderError(
        InvalidRestaurantOrderReason.EmptyCookId,
      );
    }
    const requestId = optionalText(input.requestId);
    if (requestId === undefined) {
      throw new InvalidRestaurantOrderError(
        InvalidRestaurantOrderReason.EmptyStartRequestId,
      );
    }
    this.apply({ trigger: "StartPreparation" });
    this.#cookId = cookId;
    this.#startRequestId = requestId;
    this.extendTimeline({ preparationStartedAt: input.startedAt });
    this.record(
      {
        type: "PreparationStarted",
        preparationStartedAt: input.startedAt,
        expectedReadyAt: this.timeline.expectedReadyAt,
        queuedAt: this.timeline.queuedAt,
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

  /**
   * The courier took a `READY` order (Pickup, reported by `delivery`): the
   * kitchen is done with it. A local fact, not published.
   */
  recordPickup(input: { pickedUpAt: Date }): void {
    this.apply({ trigger: "RecordPickup" });
    this.extendTimeline({ pickedUpAt: input.pickedUpAt });
    this.record(
      { type: "RestaurantOrderPickedUp", pickedUpAt: input.pickedUpAt },
      input.pickedUpAt,
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
