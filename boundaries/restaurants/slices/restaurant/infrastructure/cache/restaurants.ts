// Cached Get Restaurant and List Restaurants (ADR 40, local ADR 3): read by the
// UI, the BFF and Place Order of orders (is_open). RestaurantOpened and
// RestaurantClosed refresh the restaurant and start a new generation of the
// list pages right after the commit; renames and new restaurants publish no
// event and are visible within the TTL. List pages are keyed and generated
// per Market (ADR 41), so the pages of two Markets never mix and an opening
// change drops only the pages of the restaurant's Market.
import { fromBinary } from "@bufbuild/protobuf";
import type { Valkey } from "iovalkey";

import {
  RestaurantClosedSchema,
  RestaurantOpenedSchema,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import {
  type CacheMetrics,
  ReadCache,
} from "../../../../pkg/infrastructure/valkey/read-cache.ts";
import type { CacheRefreshes } from "../../../../pkg/infrastructure/valkey/refresh.ts";
import type { RestaurantPublicReader } from "../../applications/get-restaurant/get-restaurant.ts";
import type { RestaurantCatalogReader } from "../../applications/list-restaurants/list-restaurants.ts";
import type { RestaurantView } from "../../applications/restaurant-view.ts";
import { MarketId } from "../../domains/restaurant/restaurant.ts";

type CacheOptions = { client: Valkey; ttlMs: number; metrics: CacheMetrics };

const hasMarket = (view: RestaurantView): boolean => {
  if (typeof view.marketId !== "string") return false;
  try {
    MarketId.of(view.marketId);
    return true;
  } catch {
    return false;
  }
};

/** Generation scope of the List Restaurants pages of one Market. */
const listScope = (marketId: string): string => `pages:${marketId}`;

export class CachedRestaurantCatalog
  implements RestaurantPublicReader, RestaurantCatalogReader
{
  readonly #source: RestaurantPublicReader & RestaurantCatalogReader;
  readonly #restaurants: ReadCache<RestaurantView>;
  readonly #pages: ReadCache<readonly RestaurantView[]>;

  constructor(
    source: RestaurantPublicReader & RestaurantCatalogReader,
    options: CacheOptions,
  ) {
    this.#source = source;
    this.#restaurants = new ReadCache({
      ...options,
      name: "restaurant",
      prefix: "restaurants:restaurant:",
      // ADR 41: an entry cached before Markets has no marketId; GetRestaurant
      // must never return an empty Market, so such an entry is a miss.
      isCurrent: hasMarket,
    });
    this.#pages = new ReadCache({
      ...options,
      name: "restaurant_list",
      prefix: "restaurants:list:",
    });
  }

  get(restaurantId: string): Promise<RestaurantView | undefined> {
    return this.#restaurants.read(restaurantId, () =>
      this.#source.get(restaurantId),
    );
  }

  /** One key per page: Market, filter, cursor and page size. */
  list(criteria: {
    marketId: string;
    includeClosed: boolean;
    after: string | undefined;
    limit: number;
  }): Promise<readonly RestaurantView[]> {
    const key = `${criteria.marketId}:${criteria.includeClosed ? "all" : "open"}:${criteria.limit}:${criteria.after ?? ""}`;
    return this.#pages.readInGeneration(listScope(criteria.marketId), key, () =>
      this.#source.list(criteria),
    );
  }

  /**
   * The restaurant as stored now; every list page of its Market is dropped.
   * The Market is read from the stored restaurant, the source of truth.
   * Restaurants are never deleted, so an unknown id only removes its key.
   */
  async refresh(restaurantId: string): Promise<void> {
    const restaurant = await this.#source.get(restaurantId);
    await this.#restaurants.refresh(restaurantId, restaurant);
    if (restaurant !== undefined) {
      await this.#pages.nextGeneration(listScope(restaurant.marketId));
    }
  }
}

/** Opening facts of restaurants.v1.restaurant-events that change the cache. */
export const restaurantCacheRefreshes = (
  catalog: CachedRestaurantCatalog,
): CacheRefreshes => ({
  [RestaurantOpenedSchema.typeName]: (payload) =>
    catalog.refresh(fromBinary(RestaurantOpenedSchema, payload).restaurantId),
  [RestaurantClosedSchema.typeName]: (payload) =>
    catalog.refresh(fromBinary(RestaurantClosedSchema, payload).restaurantId),
});
