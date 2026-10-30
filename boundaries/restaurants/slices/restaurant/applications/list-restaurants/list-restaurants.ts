import type { Clock } from "../../../../pkg/usecase/clock.ts";
import { MarketId } from "../../domains/restaurant/restaurant.ts";
import {
  type AcceptingCatalogRestaurantView,
  acceptanceAt,
  type CatalogRestaurantView,
} from "../restaurant-view/restaurant-view.ts";

export const DEFAULT_RESTAURANT_PAGE_SIZE = 50;
export const MAX_RESTAURANT_PAGE_SIZE = 100;

export type ListRestaurants = Readonly<{
  /** Required (ADR 41): only restaurants of this Market are listed. */
  marketId: string;
  /**
   * Include restaurants not accepting orders; by default only restaurants
   * opened by the flag and inside their schedule now are listed (FR-205).
   */
  includeClosed?: boolean;
  /** Page size, 1..100; defaults to 50. */
  limit?: number;
  /** Cursor returned by the previous page. */
  cursor?: string;
}>;

export type ListRestaurantsResult = Readonly<{
  restaurants: readonly AcceptingCatalogRestaurantView[];
  nextCursor: string | undefined;
}>;

/**
 * Query model port. Returns restaurants ordered by a stable key after `after`;
 * without `includeClosed` only restaurants opened by the flag.
 */
export interface RestaurantCatalogReader {
  list(criteria: {
    marketId: string;
    includeClosed: boolean;
    after: string | undefined;
    limit: number;
  }): Promise<readonly CatalogRestaurantView[]>;
}

export class InvalidPageSizeError extends Error {
  readonly code = "invalid_page_size";

  constructor(limit: number) {
    super(
      `page size must be an integer in 1..${MAX_RESTAURANT_PAGE_SIZE}, got ${limit}`,
    );
    this.name = "InvalidPageSizeError";
  }
}

/**
 * Lists restaurants of one Market available for selection: opened by the flag
 * and inside the schedule at the time of the read (FR-205). The schedule is
 * not a stored criterion, so pages of open restaurants are read until the
 * page is full or the restaurants run out.
 */
export class ListRestaurantsHandler {
  readonly #reader: RestaurantCatalogReader;
  readonly #clock: Clock;

  constructor(dependencies: { reader: RestaurantCatalogReader; clock: Clock }) {
    this.#reader = dependencies.reader;
    this.#clock = dependencies.clock;
  }

  async execute(query: ListRestaurants): Promise<ListRestaurantsResult> {
    const marketId = MarketId.of(query.marketId);
    const limit = query.limit ?? DEFAULT_RESTAURANT_PAGE_SIZE;
    if (
      !Number.isInteger(limit) ||
      limit < 1 ||
      limit > MAX_RESTAURANT_PAGE_SIZE
    ) {
      throw new InvalidPageSizeError(limit);
    }
    const includeClosed = query.includeClosed ?? false;
    const now = this.#clock.now();
    const page: AcceptingCatalogRestaurantView[] = [];
    let after = query.cursor;
    for (;;) {
      const read = await this.#reader.list({
        marketId: marketId.value,
        includeClosed,
        after,
        limit: limit + 1,
      });
      for (const restaurant of read) {
        const view = { ...restaurant, ...acceptanceAt(restaurant, now) };
        if (includeClosed || view.acceptingOrders) page.push(view);
      }
      if (page.length > limit || read.length <= limit) break;
      after = read.at(-1)?.restaurantId;
    }
    const restaurants = page.slice(0, limit);
    const hasMore = page.length > limit;
    return {
      restaurants,
      nextCursor: hasMore ? restaurants.at(-1)?.restaurantId : undefined,
    };
  }
}
