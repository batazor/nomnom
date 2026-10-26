// Cached Get Restaurant and List Restaurants (ADR 40, local ADR 3): read by the
// UI, the BFF and Place Order of orders (is_open). RestaurantOpened and
// RestaurantClosed refresh the restaurant and start a new generation of the
// list pages right after the commit; renames and new restaurants publish no
// event and are visible within the TTL.
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

type CacheOptions = { client: Valkey; ttlMs: number; metrics: CacheMetrics };

/** Generation scope of every List Restaurants page. */
const LIST_SCOPE = "pages";

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
    return this.#pages.readInGeneration(LIST_SCOPE, key, () =>
      this.#source.list(criteria),
    );
  }

  /** The restaurant as stored now; every list page is dropped. */
  async refresh(restaurantId: string): Promise<void> {
    await this.#restaurants.refresh(
      restaurantId,
      await this.#source.get(restaurantId),
    );
    await this.#pages.nextGeneration(LIST_SCOPE);
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
