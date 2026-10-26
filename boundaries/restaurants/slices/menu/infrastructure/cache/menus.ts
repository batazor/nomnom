// Cached Get Menu (ADR 40, local ADR 3): the public menu of a restaurant is the
// hottest read of restaurants (UI, BFF, Place Order of orders). Keyed by
// restaurant id; a menu change is visible within the TTL.
import type { Valkey } from "iovalkey";

import {
  type CacheMetrics,
  ReadCache,
} from "../../../../pkg/infrastructure/valkey/read-cache.ts";
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
}
