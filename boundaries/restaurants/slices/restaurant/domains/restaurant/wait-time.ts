// Wait Time Statistics of a restaurant (FR-644, FR-645, FR-657): a read model
// built from the restaurant's own Restaurant Order facts, not part of the
// Restaurant aggregate. The Cook Pool does not take part in it.

export type WaitLevel = "NORMAL" | "ELEVATED";

/**
 * A Restaurant Order that became ready: its time from joining the Kitchen
 * Queue to Ready (FR-644).
 */
export type CompletedPreparation = Readonly<{
  orderId: string;
  readyAt: Date;
  durationMs: number;
}>;

/**
 * A queued Restaurant Order that is neither ready nor dropped yet. An
 * accepted scheduled order waiting for its start is not open (FR-644).
 */
export type OpenPreparation = Readonly<{ orderId: string; queuedAt: Date }>;

export type WaitTimeStatistics = Readonly<{
  /** Latest completed preparations, oldest first, at most the sample size. */
  completed: readonly CompletedPreparation[];
  open: readonly OpenPreparation[];
  level: WaitLevel;
  /** Number of level changes: the version of RestaurantWaitLevelChanged. */
  revision: number;
}>;

/** Statistics of a restaurant that has had no queued order yet. */
export const NO_WAIT_TIME_STATISTICS: WaitTimeStatistics = Object.freeze({
  completed: Object.freeze([]),
  open: Object.freeze([]),
  level: "NORMAL",
  revision: 0,
});

export type WaitEstimate = Readonly<{
  /** Median of the sample, or the preparation time until it is complete. */
  baselineMs: number;
  /** Larger of the recent median and the age of the oldest open order. */
  currentMs: number;
}>;

export type WaitLevelChanged = Readonly<{
  type: "RestaurantWaitLevelChanged";
  restaurantId: string;
  marketId: string;
  elevated: boolean;
  occurredAt: Date;
}>;

const median = (values: readonly number[]): number => {
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 1
    ? (sorted[middle] as number)
    : ((sorted[middle - 1] as number) + (sorted[middle] as number)) / 2;
};

export class InvalidWaitTimePolicyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "InvalidWaitTimePolicyError";
  }
}

/**
 * Parameters of the wait estimate. `preparationTimeMs` is the baseline until
 * a restaurant has `sampleSize` completed preparations.
 */
export class WaitTimePolicy {
  readonly sampleSize: number;
  readonly recentWindowMs: number;
  readonly elevatedFactor: number;
  readonly normalFactor: number;
  readonly preparationTimeMs: number;

  constructor(parameters: {
    sampleSize: number;
    recentWindowMs: number;
    elevatedFactor: number;
    normalFactor: number;
    preparationTimeMs: number;
  }) {
    if (
      !Number.isSafeInteger(parameters.sampleSize) ||
      parameters.sampleSize < 1
    ) {
      throw new InvalidWaitTimePolicyError(
        "sample size must be a positive integer",
      );
    }
    for (const [name, value] of [
      ["recent window", parameters.recentWindowMs],
      ["preparation time", parameters.preparationTimeMs],
    ] as const) {
      if (!Number.isFinite(value) || value <= 0) {
        throw new InvalidWaitTimePolicyError(
          `${name} must be a positive duration`,
        );
      }
    }
    if (
      !Number.isFinite(parameters.normalFactor) ||
      parameters.normalFactor <= 0
    ) {
      throw new InvalidWaitTimePolicyError("normal factor must be positive");
    }
    if (
      !Number.isFinite(parameters.elevatedFactor) ||
      parameters.elevatedFactor <= parameters.normalFactor
    ) {
      throw new InvalidWaitTimePolicyError(
        "elevated factor must be greater than the normal factor",
      );
    }
    this.sampleSize = parameters.sampleSize;
    this.recentWindowMs = parameters.recentWindowMs;
    this.elevatedFactor = parameters.elevatedFactor;
    this.normalFactor = parameters.normalFactor;
    this.preparationTimeMs = parameters.preparationTimeMs;
  }

  estimate(statistics: WaitTimeStatistics, now: Date): WaitEstimate {
    const complete = statistics.completed.length >= this.sampleSize;
    const baselineMs = complete
      ? median(statistics.completed.map((entry) => entry.durationMs))
      : this.preparationTimeMs;
    const since = now.getTime() - this.recentWindowMs;
    const recent = statistics.completed
      .filter((entry) => entry.readyAt.getTime() >= since)
      .map((entry) => entry.durationMs);
    const recentMs =
      complete && recent.length > 0 ? median(recent) : baselineMs;
    const oldestAgeMs = Math.max(
      0,
      ...statistics.open.map(
        (entry) => now.getTime() - entry.queuedAt.getTime(),
      ),
    );
    return { baselineMs, currentMs: Math.max(recentMs, oldestAgeMs) };
  }

  /** Level after the estimate; between the two thresholds it stays. */
  level(statistics: WaitTimeStatistics, now: Date): WaitLevel {
    const { baselineMs, currentMs } = this.estimate(statistics, now);
    if (currentMs > baselineMs * this.elevatedFactor) return "ELEVATED";
    if (currentMs <= baselineMs * this.normalFactor) return "NORMAL";
    return statistics.level;
  }

  /**
   * Records an order that joined the Kitchen Queue at `queuedAt`; an order
   * already open or completed changes nothing (a repeated fact, or the
   * Preparation Started of an order its Acceptance already opened).
   */
  queued(
    statistics: WaitTimeStatistics,
    orderId: string,
    queuedAt: Date,
  ): WaitTimeStatistics {
    if (
      statistics.open.some((entry) => entry.orderId === orderId) ||
      statistics.completed.some((entry) => entry.orderId === orderId)
    ) {
      return statistics;
    }
    return {
      ...statistics,
      open: [...statistics.open, { orderId, queuedAt }],
    };
  }

  /** Moves an open order to the sample; unknown orders change nothing. */
  ready(
    statistics: WaitTimeStatistics,
    orderId: string,
    readyAt: Date,
  ): WaitTimeStatistics {
    const entry = statistics.open.find((open) => open.orderId === orderId);
    if (entry === undefined) return statistics;
    const completed = [
      ...statistics.completed,
      {
        orderId,
        readyAt,
        durationMs: Math.max(0, readyAt.getTime() - entry.queuedAt.getTime()),
      },
    ].slice(-this.sampleSize);
    return {
      ...statistics,
      completed,
      open: statistics.open.filter((open) => open.orderId !== orderId),
    };
  }

  /** A cancelled or failed order leaves without a duration. */
  dropped(statistics: WaitTimeStatistics, orderId: string): WaitTimeStatistics {
    if (!statistics.open.some((open) => open.orderId === orderId)) {
      return statistics;
    }
    return {
      ...statistics,
      open: statistics.open.filter((open) => open.orderId !== orderId),
    };
  }
}
