export const InvalidScheduledOrderSettingsReason = {
  NonPositiveSlotCapacity: "non_positive_slot_capacity",
  NegativeOpeningLead: "negative_opening_lead",
  NegativeClosingGrace: "negative_closing_grace",
} as const;

export type InvalidScheduledOrderSettingsReason =
  (typeof InvalidScheduledOrderSettingsReason)[keyof typeof InvalidScheduledOrderSettingsReason];

/** Rejected settings of scheduled orders (FR-672). */
export class InvalidScheduledOrderSettingsError extends Error {
  readonly code = "invalid_scheduled_order_settings";
  readonly reason: InvalidScheduledOrderSettingsReason;

  constructor(reason: InvalidScheduledOrderSettingsReason) {
    super(`invalid scheduled order settings: ${reason}`);
    this.name = "InvalidScheduledOrderSettingsError";
    this.reason = reason;
  }
}

/** Stored form of {@link ScheduledOrderSettings}; durations in milliseconds. */
export type ScheduledOrderSettingsValue = Readonly<{
  /** Scheduled orders the restaurant takes per Delivery Window. */
  slotCapacity: number;
  /** The first window of a period starts no earlier than opening + lead. */
  openingLeadMs: number;
  /** The last window of a period starts no later than closing + grace. */
  closingGraceMs: number;
}>;

/**
 * Settings of scheduled orders of a restaurant (FR-672, FR-703), set and
 * cleared only together. A restaurant without them takes no scheduled orders.
 */
export class ScheduledOrderSettings {
  readonly slotCapacity: number;
  readonly openingLeadMs: number;
  readonly closingGraceMs: number;

  private constructor(value: ScheduledOrderSettingsValue) {
    this.slotCapacity = value.slotCapacity;
    this.openingLeadMs = value.openingLeadMs;
    this.closingGraceMs = value.closingGraceMs;
  }

  static of(value: ScheduledOrderSettingsValue): ScheduledOrderSettings {
    if (!Number.isSafeInteger(value.slotCapacity) || value.slotCapacity < 1) {
      throw new InvalidScheduledOrderSettingsError(
        InvalidScheduledOrderSettingsReason.NonPositiveSlotCapacity,
      );
    }
    if (!Number.isSafeInteger(value.openingLeadMs) || value.openingLeadMs < 0) {
      throw new InvalidScheduledOrderSettingsError(
        InvalidScheduledOrderSettingsReason.NegativeOpeningLead,
      );
    }
    if (
      !Number.isSafeInteger(value.closingGraceMs) ||
      value.closingGraceMs < 0
    ) {
      throw new InvalidScheduledOrderSettingsError(
        InvalidScheduledOrderSettingsReason.NegativeClosingGrace,
      );
    }
    return new ScheduledOrderSettings(value);
  }

  equals(other: ScheduledOrderSettings): boolean {
    return (
      this.slotCapacity === other.slotCapacity &&
      this.openingLeadMs === other.openingLeadMs &&
      this.closingGraceMs === other.closingGraceMs
    );
  }

  toValue(): ScheduledOrderSettingsValue {
    return {
      slotCapacity: this.slotCapacity,
      openingLeadMs: this.openingLeadMs,
      closingGraceMs: this.closingGraceMs,
    };
  }
}
