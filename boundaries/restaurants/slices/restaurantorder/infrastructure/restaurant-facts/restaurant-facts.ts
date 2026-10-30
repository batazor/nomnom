// Read adapters of the facts `restaurantorder` takes from the `restaurant` and
// `menu` slices. They read the same collections through the owning slices'
// repositories, so they join the active Unit of Work like any other read.
import type { MenuRepository } from "../../../menu/domains/menu/menu.ts";
import type {
  MarketId,
  RestaurantRepository,
} from "../../../restaurant/domains/restaurant/restaurant.ts";
import type {
  AcceptanceFacts,
  AcceptanceFactsReader,
} from "../../applications/acceptance/accept-order/accept-order.ts";
import type { RestaurantMarkets } from "../../applications/acceptance/receive-order-request/receive-order-request.ts";

/**
 * Existence and Market of a restaurant, read from the Restaurant aggregate.
 * `exists` serves the `CookRestaurants` port of the `cook` slice, which depends
 * on this slice and not the other way round.
 */
export class RepositoryRestaurantExistence implements RestaurantMarkets {
  readonly #restaurants: Pick<RestaurantRepository, "get">;

  constructor(restaurants: Pick<RestaurantRepository, "get">) {
    this.#restaurants = restaurants;
  }

  async exists(restaurantId: string): Promise<boolean> {
    return (await this.#restaurants.get(restaurantId)) !== undefined;
  }

  async marketOf(restaurantId: string): Promise<MarketId | undefined> {
    return (await this.#restaurants.get(restaurantId))?.marketId;
  }
}

export class RepositoryAcceptanceFactsReader implements AcceptanceFactsReader {
  readonly #restaurants: Pick<RestaurantRepository, "get">;
  readonly #menus: Pick<MenuRepository, "findByRestaurantId">;

  constructor(dependencies: {
    restaurants: Pick<RestaurantRepository, "get">;
    menus: Pick<MenuRepository, "findByRestaurantId">;
  }) {
    this.#restaurants = dependencies.restaurants;
    this.#menus = dependencies.menus;
  }

  async read(
    restaurantId: string,
    menuItemIds: readonly string[],
  ): Promise<AcceptanceFacts | undefined> {
    const restaurant = await this.#restaurants.get(restaurantId);
    if (restaurant === undefined) {
      return undefined;
    }
    const menu = await this.#menus.findByRestaurantId(restaurantId);
    const available = new Map<string, boolean>();
    for (const menuItemId of menuItemIds) {
      const item = menu?.findItem(menuItemId);
      if (item !== undefined) {
        available.set(menuItemId, item.isAvailable);
      }
    }
    return { isOpen: restaurant.isOpen, available };
  }
}
