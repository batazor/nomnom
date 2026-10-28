export const InvalidOrderScheduleReason = {
  Incomplete: "incomplete",
  EmptyWindow: "empty_window",
  PlannedReadyAtNotBeforeWindow: "planned_ready_at_not_before_window",
} as const;

export type InvalidOrderScheduleReason =
  (typeof InvalidOrderScheduleReason)[keyof typeof InvalidOrderScheduleReason];

/** Rejected schedule of a Receive Order Request (FR-683). */
export class InvalidOrderScheduleError extends Error {
  readonly code = "invalid_order_schedule";
  readonly reason: InvalidOrderScheduleReason;

  constructor(reason: InvalidOrderScheduleReason) {
    super(`invalid order schedule: ${reason}`);
    this.name = "InvalidOrderScheduleError";
    this.reason = reason;
  }
}

/** Stored form of {@link OrderSchedule}. */
export type OrderScheduleValue = Readonly<{
  windowStart: Date;
  windowEnd: Date;
  plannedReadyAt: Date;
}>;

/**
 * Delivery Window and Planned Ready At of a scheduled order (FR-683): data of
 * the Restaurant Order, not a State. Planned Ready At is before the window,
 * the window is not empty.
 */
export class OrderSchedule {
  readonly windowStart: Date;
  readonly windowEnd: Date;
  readonly plannedReadyAt: Date;

  private constructor(value: OrderScheduleValue) {
    this.windowStart = value.windowStart;
    this.windowEnd = value.windowEnd;
    this.plannedReadyAt = value.plannedReadyAt;
  }

  /** Every field is required: a partial block is invalid. */
  static of(
    input: Readonly<{
      windowStart?: Date;
      windowEnd?: Date;
      plannedReadyAt?: Date;
    }>,
  ): OrderSchedule {
    const { windowStart, windowEnd, plannedReadyAt } = input;
    if (
      windowStart === undefined ||
      windowEnd === undefined ||
      plannedReadyAt === undefined
    ) {
      throw new InvalidOrderScheduleError(
        InvalidOrderScheduleReason.Incomplete,
      );
    }
    if (windowEnd.getTime() <= windowStart.getTime()) {
      throw new InvalidOrderScheduleError(
        InvalidOrderScheduleReason.EmptyWindow,
      );
    }
    if (plannedReadyAt.getTime() >= windowStart.getTime()) {
      throw new InvalidOrderScheduleError(
        InvalidOrderScheduleReason.PlannedReadyAtNotBeforeWindow,
      );
    }
    return new OrderSchedule({ windowStart, windowEnd, plannedReadyAt });
  }

  equals(other: OrderSchedule): boolean {
    return (
      this.windowStart.getTime() === other.windowStart.getTime() &&
      this.windowEnd.getTime() === other.windowEnd.getTime() &&
      this.plannedReadyAt.getTime() === other.plannedReadyAt.getTime()
    );
  }

  toValue(): OrderScheduleValue {
    return {
      windowStart: this.windowStart,
      windowEnd: this.windowEnd,
      plannedReadyAt: this.plannedReadyAt,
    };
  }
}
