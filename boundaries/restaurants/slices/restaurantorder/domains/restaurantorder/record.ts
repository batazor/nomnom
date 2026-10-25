import type { EventPayload, RestaurantOrderEvent } from "./events.ts";
import {
  type RestaurantOrderState,
  resolveTransition,
  type TransitionRequest,
} from "./fsm.ts";
import type { PreparationItem } from "./items.ts";

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

/**
 * Fields, snapshot and recorded Domain Events of `RestaurantOrder`. Only the
 * aggregate changes them, through the protected methods; every state change
 * goes through {@link resolveTransition}.
 */
export abstract class RestaurantOrderRecord {
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

  protected constructor(
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

  protected apply(request: TransitionRequest): void {
    const outcome = resolveTransition(this.#state, request);
    if (!outcome.ok) {
      throw outcome.error;
    }
    this.#state = outcome.to;
  }

  protected extendTimeline(timeline: RestaurantOrderTimeline): void {
    this.#timeline = { ...this.#timeline, ...timeline };
  }

  protected setRejectionReason(reason: string | undefined): void {
    this.#rejectionReason = reason;
  }

  protected setFailureReason(reason: string): void {
    this.#failureReason = reason;
  }

  protected setCancellation(
    cancellation: NonNullable<RestaurantOrderSnapshot["cancellation"]>,
  ): void {
    this.#cancellation = cancellation;
  }

  protected record(
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
}
