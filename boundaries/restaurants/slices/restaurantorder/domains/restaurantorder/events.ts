import type { RestaurantOrderState } from "./fsm.ts";

/** Fields shared by every Domain Event of `RestaurantOrder`. */
export type EventBase = Readonly<{
  restaurantOrderId: string;
  orderId: string;
  restaurantId: string;
  /** Market of the restaurant that received the order (ADR 41). */
  marketId: string;
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

export type EventPayload<E> = E extends EventBase
  ? Omit<E, keyof EventBase>
  : never;
