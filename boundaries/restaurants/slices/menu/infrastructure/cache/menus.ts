// Cached Get Menu (ADR 40, local ADR 3): the public menu of a restaurant is the
// hottest read of restaurants (UI, BFF, Place Order of orders). Keyed by
// restaurant id. MenuItemAdded, MenuItemUpdated and
// MenuItemAvailabilityChanged refresh the menu right after the commit.
import { fromBinary } from "@bufbuild/protobuf";
import type { Valkey } from "iovalkey";

import {
  MenuItemAddedSchema,
  MenuItemAvailabilityChangedSchema,
  MenuItemUpdatedSchema,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
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
  [MenuItemAddedSchema.typeName]: (payload) =>
    menus.refresh(fromBinary(MenuItemAddedSchema, payload).restaurantId),
  [MenuItemUpdatedSchema.typeName]: (payload) =>
    menus.refresh(fromBinary(MenuItemUpdatedSchema, payload).restaurantId),
  [MenuItemAvailabilityChangedSchema.typeName]: (payload) =>
    menus.refresh(
      fromBinary(MenuItemAvailabilityChangedSchema, payload).restaurantId,
    ),
});
