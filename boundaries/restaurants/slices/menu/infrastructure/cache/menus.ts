// Cached Get Menu (ADR 40, local ADR 3): the public menu of a restaurant is the
// hottest read of restaurants (UI, BFF, Place Order of orders). Keyed by
// restaurant id. MenuItemAvailabilityChanged refreshes the menu right after
// the commit; price and name changes publish no event and are visible within
// the TTL.
import { fromBinary } from "@bufbuild/protobuf";
import type { Valkey } from "iovalkey";

import { MenuItemAvailabilityChangedSchema } from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import {
  type CacheMetrics,
  ReadCache,
} from "../../../../pkg/infrastructure/valkey/read-cache.ts";
import type { CacheRefreshes } from "../../../../pkg/infrastructure/valkey/refresh.ts";
import type { MenuReader } from "../../applications/get-menu/get-menu.ts";
import type { MenuView } from "../../applications/menu-support.ts";

export class CachedMenuReader implements MenuReader {
  readonly #source: MenuReader;
  readonly #cache: ReadCache<MenuView>;

  constructor(
    source: MenuReader,
    options: { client: Valkey; ttlMs: number; metrics: CacheMetrics },
  ) {
    this.#source = source;
    this.#cache = new ReadCache<MenuView>({
      ...options,
      name: "menu",
      prefix: "restaurants:menu:",
    });
  }

  getByRestaurantId(restaurantId: string): Promise<MenuView | undefined> {
    return this.#cache.read(restaurantId, () =>
      this.#source.getByRestaurantId(restaurantId),
    );
  }

  /** The menu of the restaurant as stored now. */
  async refresh(restaurantId: string): Promise<void> {
    await this.#cache.refresh(
      restaurantId,
      await this.#source.getByRestaurantId(restaurantId),
    );
  }
}

/** Facts of restaurants.v1.menu-events that change the cache. */
export const menuCacheRefreshes = (
  menus: CachedMenuReader,
): CacheRefreshes => ({
  [MenuItemAvailabilityChangedSchema.typeName]: (payload) =>
    menus.refresh(
      fromBinary(MenuItemAvailabilityChangedSchema, payload).restaurantId,
    ),
});
