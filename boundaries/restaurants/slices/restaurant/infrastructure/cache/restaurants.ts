// Cached Get Restaurant and List Restaurants (ADR 40, local ADR 3): read by the
// UI, the BFF and Place Order of orders (is_open), changed rarely. A change —
// opening, closing, a new restaurant — is visible within the TTL.
import type { Valkey } from "iovalkey";

import {
  type CacheMetrics,
  ReadCache,
} from "../../../../pkg/infrastructure/valkey/read-cache.ts";
import type { RestaurantPublicReader } from "../../applications/get-restaurant/get-restaurant.ts";
import type { RestaurantCatalogReader } from "../../applications/list-restaurants/list-restaurants.ts";
import type { RestaurantView } from "../../applications/restaurant-view.ts";

type CacheOptions = { client: Valkey; ttlMs: number; metrics: CacheMetrics };

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

  /** One key per page: filter, cursor and page size. */
  list(criteria: {
    includeClosed: boolean;
    after: string | undefined;
    limit: number;
  }): Promise<readonly RestaurantView[]> {
    const key = `${criteria.includeClosed ? "all" : "open"}:${criteria.limit}:${criteria.after ?? ""}`;
    return this.#pages.read(key, () => this.#source.list(criteria));
  }
}
