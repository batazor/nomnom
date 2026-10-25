import {
  type AcceptanceEligibility,
  type CancellationPolicy,
  RestaurantOrderState,
  resolveTransition,
  type TransitionRequest,
} from "./fsm.ts";
import { InvalidQuantityError, Quantity } from "./vo/quantity.ts";

export {
  type AcceptanceEligibility,
  AlreadyInStateError,
  type CancellationPolicy,
  GuardFailedError,
  InvalidTransitionError,
  RestaurantOrderState,
} from "./fsm.ts";

/** Position of a Restaurant Order in kitchen work; it carries no price. */
export type PreparationItem = Readonly<{
  menuItemId: string;
  quantity: Quantity;
  /** Opaque ids of selected options; the Modifier model is not agreed yet. */
  options: readonly string[];
}>;

export type PreparationItemInput = Readonly<{
  menuItemId: string;
  quantity: number;
  options?: readonly string[];
}>;

export type RestaurantOrderTimeline = Readonly<{
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

type EventBase = Readonly<{
  restaurantOrderId: string;
  orderId: string;
  restaurantId: string;
  occurredAt: Date;
}>;

export type RestaurantOrderReceived = EventBase &
  Readonly<{ type: "RestaurantOrderReceived" }>;
export type RestaurantOrderAccepted = EventBase &
  Readonly<{
    type: "RestaurantOrderAccepted";
    acceptedAt: Date;
    expectedReadyAt: Date;
  }>;
export type RestaurantOrderRejected = EventBase &
  Readonly<{
    type: "RestaurantOrderRejected";
    stage: RestaurantOrderState;
    reason: string | undefined;
  }>;
export type RestaurantOrderQueued = EventBase &
  Readonly<{ type: "RestaurantOrderQueued"; queuedAt: Date }>;
export type PreparationStarted = EventBase &
  Readonly<{
    type: "PreparationStarted";
    preparationStartedAt: Date;
    expectedReadyAt: Date | undefined;
  }>;
export type RestaurantOrderAssembled = EventBase &
  Readonly<{ type: "RestaurantOrderAssembled" }>;
export type RestaurantOrderReady = EventBase &
  Readonly<{ type: "RestaurantOrderReady"; readyAt: Date }>;
export type PreparationDelayed = EventBase &
  Readonly<{
    type: "PreparationDelayed";
    expectedReadyAt: Date;
    previousExpectedReadyAt: Date | undefined;
    reason: string | undefined;
  }>;
export type RestaurantOrderFailed = EventBase &
  Readonly<{
    type: "RestaurantOrderFailed";
    stage: RestaurantOrderState;
    reason: string;
  }>;
export type RestaurantOrderCancelled = EventBase &
  Readonly<{
    type: "RestaurantOrderCancelled";
    stage: RestaurantOrderState;
    preparationStarted: boolean;
    reason: string | undefined;
  }>;

export type RestaurantOrderEvent =
  | RestaurantOrderReceived
  | RestaurantOrderAccepted
  | RestaurantOrderRejected
  | RestaurantOrderQueued
  | PreparationStarted
  | RestaurantOrderAssembled
  | RestaurantOrderReady
  | PreparationDelayed
  | RestaurantOrderFailed
  | RestaurantOrderCancelled;

export type RestaurantOrderEventType = RestaurantOrderEvent["type"];

type EventPayload<E> = E extends EventBase ? Omit<E, keyof EventBase> : never;

export const InvalidRestaurantOrderReason = {
  EmptyItems: "empty_items",
  EmptyMenuItemId: "empty_menu_item_id",
  InvalidQuantity: "invalid_quantity",
  DuplicateItem: "duplicate_item",
  EmptyFailureReason: "empty_failure_reason",
} as const;

export type InvalidRestaurantOrderReason =
  (typeof InvalidRestaurantOrderReason)[keyof typeof InvalidRestaurantOrderReason];

export class InvalidRestaurantOrderError extends Error {
  readonly code = "invalid_restaurant_order";
  readonly reason: InvalidRestaurantOrderReason;

  constructor(reason: InvalidRestaurantOrderReason) {
    super(`invalid restaurant order: ${reason}`);
    this.name = "InvalidRestaurantOrderError";
    this.reason = reason;
  }
}

/** A Restaurant Order already exists for the contract `orderId`. */
export class DuplicateOrderIdError extends Error {
  readonly code = "duplicate_order_id";
  readonly orderId: string;

  constructor(orderId: string) {
    super(`a restaurant order for order ${orderId} already exists`);
    this.name = "DuplicateOrderIdError";
    this.orderId = orderId;
  }
}

const itemKey = (item: {
  menuItemId: string;
  options: readonly string[];
}): string => JSON.stringify([item.menuItemId, item.options]);

const toPreparationItems = (
  inputs: readonly PreparationItemInput[],
): PreparationItem[] => {
  if (inputs.length === 0) {
    throw new InvalidRestaurantOrderError(
      InvalidRestaurantOrderReason.EmptyItems,
    );
  }
  const keys = new Set<string>();
  return inputs.map((input) => {
    const menuItemId = input.menuItemId.trim();
    if (menuItemId.length === 0) {
      throw new InvalidRestaurantOrderError(
        InvalidRestaurantOrderReason.EmptyMenuItemId,
      );
    }
    let quantity: Quantity;
    try {
      quantity = Quantity.of(input.quantity);
    } catch (error) {
      if (error instanceof InvalidQuantityError) {
        throw new InvalidRestaurantOrderError(
          InvalidRestaurantOrderReason.InvalidQuantity,
        );
      }
      throw error;
    }
    const options = [...new Set(input.options ?? [])].sort();
    const key = itemKey({ menuItemId, options });
    if (keys.has(key)) {
      throw new InvalidRestaurantOrderError(
        InvalidRestaurantOrderReason.DuplicateItem,
      );
    }
    keys.add(key);
    return { menuItemId, quantity, options };
  });
};

const optionalText = (text: string | undefined): string | undefined => {
  const normalized = text?.trim();
  return normalized === undefined || normalized.length === 0
    ? undefined
    : normalized;
};

const compositionKey = (items: readonly PreparationItem[]): string =>
  JSON.stringify(
    items
      .map((item) => [itemKey(item), item.quantity.value] as const)
      .sort(([left], [right]) => (left < right ? -1 : left > right ? 1 : 0)),
  );

/**
 * Aggregate root of the kitchen lifecycle of one `orders.Order` inside
 * `restaurants`. `Kitchen Order` is its role after Acceptance, not a separate
 * aggregate. Every state change goes through {@link resolveTransition}; domain
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
    order.#record({ type: "RestaurantOrderReceived" }, input.receivedAt);
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

  /** Whether a repeated Receive Order Request carries the same request. */
  matchesRequest(input: {
    restaurantId: string;
    items: readonly PreparationItemInput[];
  }): boolean {
    if (input.restaurantId !== this.#restaurantId) {
      return false;
    }
    let items: PreparationItem[];
    try {
      items = toPreparationItems(input.items);
    } catch {
      return false;
    }
    return compositionKey(items) === compositionKey(this.#items);
  }

  accept(input: {
    acceptedAt: Date;
    expectedReadyAt: Date;
    eligibility: AcceptanceEligibility;
  }): void {
    this.#apply({ trigger: "Accept", eligibility: input.eligibility });
    this.#timeline = {
      ...this.#timeline,
      acceptedAt: input.acceptedAt,
      expectedReadyAt: input.expectedReadyAt,
    };
    this.#record(
      {
        type: "RestaurantOrderAccepted",
        acceptedAt: input.acceptedAt,
        expectedReadyAt: input.expectedReadyAt,
      },
      input.acceptedAt,
    );
  }

  reject(input: { rejectedAt: Date; reason?: string }): void {
    const stage = this.#state;
    this.#apply({ trigger: "Reject" });
    this.#rejectionReason = optionalText(input.reason);
    this.#record(
      {
        type: "RestaurantOrderRejected",
        stage,
        reason: this.#rejectionReason,
      },
      input.rejectedAt,
    );
  }

  queue(input: { queuedAt: Date }): void {
    this.#apply({ trigger: "Queue" });
    this.#timeline = { ...this.#timeline, queuedAt: input.queuedAt };
    this.#record(
      { type: "RestaurantOrderQueued", queuedAt: input.queuedAt },
      input.queuedAt,
    );
  }

  startPreparation(input: { startedAt: Date }): void {
    this.#apply({ trigger: "StartPreparation" });
    this.#timeline = {
      ...this.#timeline,
      preparationStartedAt: input.startedAt,
    };
    this.#record(
      {
        type: "PreparationStarted",
        preparationStartedAt: input.startedAt,
        expectedReadyAt: this.#timeline.expectedReadyAt,
      },
      input.startedAt,
    );
  }

  /**
   * Kitchen confirms that every required item is prepared and assembled.
   * Without item-level model the command itself is that statement.
   */
  markAssembled(input: { assembledAt: Date }): void {
    this.#apply({ trigger: "MarkAssembled" });
    this.#record({ type: "RestaurantOrderAssembled" }, input.assembledAt);
  }

  markReady(input: { readyAt: Date }): void {
    this.#apply({ trigger: "MarkReady" });
    this.#timeline = { ...this.#timeline, readyAt: input.readyAt };
    this.#record(
      { type: "RestaurantOrderReady", readyAt: input.readyAt },
      input.readyAt,
    );
  }

  reportDelay(input: {
    reportedAt: Date;
    expectedReadyAt: Date;
    reason?: string;
  }): void {
    const previousExpectedReadyAt = this.#timeline.expectedReadyAt;
    this.#apply({
      trigger: "ReportDelay",
      now: input.reportedAt,
      currentExpectedReadyAt: previousExpectedReadyAt,
      requestedExpectedReadyAt: input.expectedReadyAt,
    });
    this.#timeline = {
      ...this.#timeline,
      expectedReadyAt: input.expectedReadyAt,
    };
    this.#record(
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
    const stage = this.#state;
    this.#apply({ trigger: "ReportFailure" });
    this.#failureReason = reason;
    this.#record(
      { type: "RestaurantOrderFailed", stage, reason },
      input.failedAt,
    );
  }

  cancel(input: {
    cancelledAt: Date;
    policy: CancellationPolicy;
    reason?: string;
  }): void {
    const stage = this.#state;
    this.#apply({ trigger: "Cancel", policy: input.policy });
    const reason = optionalText(input.reason);
    this.#cancellation = { stage, reason };
    this.#record(
      {
        type: "RestaurantOrderCancelled",
        stage,
        preparationStarted: this.#timeline.preparationStartedAt !== undefined,
        reason,
      },
      input.cancelledAt,
    );
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

  #apply(request: TransitionRequest): void {
    const outcome = resolveTransition(this.#state, request);
    if (!outcome.ok) {
      throw outcome.error;
    }
    this.#state = outcome.to;
  }

  #record(payload: EventPayload<RestaurantOrderEvent>, occurredAt: Date): void {
    this.#events.push({
      ...payload,
      restaurantOrderId: this.#id,
      orderId: this.#orderId,
      restaurantId: this.#restaurantId,
      occurredAt,
    } as RestaurantOrderEvent);
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
