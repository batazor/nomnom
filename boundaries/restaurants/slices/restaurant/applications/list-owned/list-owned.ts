import { PermissionDeniedError } from "../../../../pkg/authz/errors.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import { MarketId } from "../../domains/restaurant/restaurant.ts";
import {
  DEFAULT_RESTAURANT_PAGE_SIZE,
  InvalidPageSizeError,
  MAX_RESTAURANT_PAGE_SIZE,
} from "../list/list.ts";
import type { RestaurantView } from "../restaurant-view/restaurant-view.ts";

export type ListOwnedRestaurants = Readonly<{
  subject: Subject;
  /** Required (ADR 41): only restaurants of this Market are listed. */
  marketId: string;
  /** Page size, 1..100; defaults to 50. */
  limit?: number;
  /** Cursor returned by the previous page. */
  cursor?: string;
}>;

export type ListOwnedRestaurantsResult = Readonly<{
  restaurants: readonly RestaurantView[];
  nextCursor: string | undefined;
}>;

/** Query model port. Returns restaurants of one owner ordered by id after `after`. */
export interface OwnedRestaurantsReader {
  listOwned(criteria: {
    ownerId: string;
    marketId: string;
    after: string | undefined;
    limit: number;
  }): Promise<readonly RestaurantView[]>;
}

/**
 * Lists open and closed restaurants of one Market owned by the calling user. `ownerId` is
 * the source of `restaurant#owner` and `restaurant#manage = owner`, so the
 * filter is the authorization: a user sees only restaurants they manage
 * without a check per restaurant. A service subject owns nothing and is denied.
 */
export class ListOwnedRestaurantsHandler {
  readonly #reader: OwnedRestaurantsReader;

  constructor(dependencies: { reader: OwnedRestaurantsReader }) {
    this.#reader = dependencies.reader;
  }

  async execute(
    query: ListOwnedRestaurants,
  ): Promise<ListOwnedRestaurantsResult> {
    if (query.subject.type !== "user") {
      throw new PermissionDeniedError("list_owned_restaurants");
    }
    const marketId = MarketId.of(query.marketId);
    const limit = query.limit ?? DEFAULT_RESTAURANT_PAGE_SIZE;
    if (
      !Number.isInteger(limit) ||
      limit < 1 ||
      limit > MAX_RESTAURANT_PAGE_SIZE
    ) {
      throw new InvalidPageSizeError(limit);
    }
    const page = await this.#reader.listOwned({
      ownerId: query.subject.id,
      marketId: marketId.value,
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
