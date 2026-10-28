import type { Restaurant } from "../../../restaurant/domains/restaurant/restaurant.ts";
import {
  InvalidDeliveryWindowError,
  InvalidDeliveryWindowReason,
} from "./errors.ts";

/** Delivery Window `[start, end)` of a scheduled order. */
export type DeliveryWindow = Readonly<{ start: Date; end: Date }>;

/** Grid of Delivery Windows (FR-673, FR-694); durations in milliseconds. */
export type DeliveryWindowGrid = Readonly<{
  widthMs: number;
  minLeadMs: number;
  maxHorizonMs: number;
}>;

/**
 * Delivery Windows a restaurant offers (FR-673, FR-703): a grid of
 * `widthMs`-wide windows aligned to UTC, starting from now + MIN_LEAD and
 * before now + MAX_HORIZON. A window is admissible only when its start lies
 * in a schedule period of the restaurant between opening + opening lead and
 * closing + closing grace; between periods there are no windows. Only
 * instants are compared, so clock changes need no special case.
 */
export class DeliveryWindows {
  readonly #grid: DeliveryWindowGrid;

  constructor(grid: DeliveryWindowGrid) {
    if (!Number.isSafeInteger(grid.widthMs) || grid.widthMs <= 0) {
      throw new Error("delivery window width must be a positive duration");
    }
    if (grid.minLeadMs >= grid.maxHorizonMs) {
      throw new Error("minimum lead must be shorter than the horizon");
    }
    this.#grid = grid;
  }

  window(start: Date): DeliveryWindow {
    return { start, end: new Date(start.getTime() + this.#grid.widthMs) };
  }

  /**
   * Admissible windows of the restaurant at `now`, by start. Empty when it
   * has no settings of scheduled orders or does not accept orders now: the
   * kitchen accepts a scheduled order at once, so one is placed only while
   * the restaurant works.
   */
  offered(restaurant: Restaurant, now: Date): DeliveryWindow[] {
    const settings = restaurant.scheduledOrders;
    if (settings === undefined || !restaurant.acceptsOrdersAt(now)) return [];
    const width = this.#grid.widthMs;
    const from =
      Math.ceil((now.getTime() + this.#grid.minLeadMs) / width) * width;
    const to = now.getTime() + this.#grid.maxHorizonMs;
    // A period that closed before `from` still admits windows up to its
    // closing grace.
    const periods = restaurant.openingPeriodsBetween(
      new Date(from - settings.closingGraceMs - 1),
      new Date(to),
    );
    const starts = new Set<number>();
    for (const period of periods) {
      const first = Math.max(
        from,
        Math.ceil((period.opensAt.getTime() + settings.openingLeadMs) / width) *
          width,
      );
      const last = period.closesAt.getTime() + settings.closingGraceMs;
      for (let start = first; start <= last && start < to; start += width) {
        starts.add(start);
      }
    }
    return [...starts]
      .sort((a, b) => a - b)
      .map((start) => this.window(new Date(start)));
  }

  /**
   * The offered window starting at `start`; throws
   * {@link InvalidDeliveryWindowError} with the first violated rule.
   */
  require(
    offered: readonly DeliveryWindow[],
    start: Date,
    now: Date,
  ): DeliveryWindow {
    const at = start.getTime();
    if (at % this.#grid.widthMs !== 0) {
      throw new InvalidDeliveryWindowError(
        InvalidDeliveryWindowReason.OffGrid,
        start,
      );
    }
    if (at < now.getTime() + this.#grid.minLeadMs) {
      throw new InvalidDeliveryWindowError(
        InvalidDeliveryWindowReason.BeforeMinimumLead,
        start,
      );
    }
    if (at >= now.getTime() + this.#grid.maxHorizonMs) {
      throw new InvalidDeliveryWindowError(
        InvalidDeliveryWindowReason.BeyondHorizon,
        start,
      );
    }
    const window = offered.find(
      (candidate) => candidate.start.getTime() === at,
    );
    if (window === undefined) {
      throw new InvalidDeliveryWindowError(
        InvalidDeliveryWindowReason.OutsideOpeningHours,
        start,
      );
    }
    return window;
  }
}
