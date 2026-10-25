import type { RestaurantView } from "../restaurant-view.ts";

export const DEFAULT_RESTAURANT_PAGE_SIZE = 50;
export const MAX_RESTAURANT_PAGE_SIZE = 100;

export type ListRestaurants = Readonly<{
  /** Include closed restaurants; by default only open restaurants are listed. */
  includeClosed?: boolean;
  /** Page size, 1..100; defaults to 50. */
  limit?: number;
  /** Cursor returned by the previous page. */
  cursor?: string;
}>;

export type ListRestaurantsResult = Readonly<{
  restaurants: readonly RestaurantView[];
  nextCursor: string | undefined;
}>;

/** Query model port. Returns restaurants ordered by a stable key after `after`. */
export interface RestaurantCatalogReader {
  list(criteria: {
    includeClosed: boolean;
    after: string | undefined;
    limit: number;
  }): Promise<readonly RestaurantView[]>;
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
 * Lists restaurants available for selection. The only availability criterion
 * known today is the restaurant opening; hours, zones and capacity are not
 * considered until specified.
 */
export class ListRestaurantsHandler {
  readonly #reader: RestaurantCatalogReader;

  constructor(dependencies: { reader: RestaurantCatalogReader }) {
    this.#reader = dependencies.reader;
  }

  async execute(query: ListRestaurants): Promise<ListRestaurantsResult> {
    const limit = query.limit ?? DEFAULT_RESTAURANT_PAGE_SIZE;
    if (
      !Number.isInteger(limit) ||
      limit < 1 ||
      limit > MAX_RESTAURANT_PAGE_SIZE
    ) {
      throw new InvalidPageSizeError(limit);
    }
    const page = await this.#reader.list({
      includeClosed: query.includeClosed ?? false,
      after: query.cursor,
      limit: limit + 1,
    });
    const restaurants = page.slice(0, limit);
    const hasMore = page.length > limit;
    return {
      restaurants,
      nextCursor: hasMore ? restaurants.at(-1)?.restaurantId : undefined,
    };
  }
}
