export const InvalidDeliveryWindowReason = {
  OffGrid: "off_grid",
  BeforeMinimumLead: "before_minimum_lead",
  BeyondHorizon: "beyond_horizon",
  OutsideOpeningHours: "outside_opening_hours",
} as const;

export type InvalidDeliveryWindowReason =
  (typeof InvalidDeliveryWindowReason)[keyof typeof InvalidDeliveryWindowReason];

/**
 * The requested window start is not a Delivery Window the restaurant offers
 * now (FR-674, FR-703): off the grid, earlier than now + MIN_LEAD, beyond the
 * horizon or outside the schedule.
 */
export class InvalidDeliveryWindowError extends Error {
  readonly code = "invalid_delivery_window";
  readonly reason: InvalidDeliveryWindowReason;

  constructor(reason: InvalidDeliveryWindowReason, windowStart: Date) {
    super(
      `delivery window ${windowStart.toISOString()} is not offered: ${reason}`,
    );
    this.name = "InvalidDeliveryWindowError";
    this.reason = reason;
  }
}

/**
 * The restaurant takes no scheduled orders now: it has no settings of
 * scheduled orders or does not accept orders at the moment (FR-674).
 * Reported to callers as SCHEDULED_ORDERS_UNAVAILABLE.
 */
export class ScheduledOrdersUnavailableError extends Error {
  readonly code = "scheduled_orders_unavailable";
  readonly restaurantId: string;

  constructor(restaurantId: string) {
    super(
      `SCHEDULED_ORDERS_UNAVAILABLE: restaurant ${restaurantId} takes no scheduled orders now`,
    );
    this.name = "ScheduledOrdersUnavailableError";
    this.restaurantId = restaurantId;
  }
}

/** The Schedule Slot holds no reservation of the order. */
export class UnknownSlotReservationError extends Error {
  readonly code = "not_found";
  readonly orderId: string;

  constructor(orderId: string) {
    super(`SlotReservation ${orderId} was not found`);
    this.name = "UnknownSlotReservationError";
    this.orderId = orderId;
  }
}
