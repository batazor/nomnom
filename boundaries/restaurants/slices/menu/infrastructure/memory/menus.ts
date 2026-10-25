import { AlreadyExistsError } from "../../../../pkg/application/errors.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import { InMemoryDocumentStore } from "../../../../pkg/infrastructure/memory/document-store.ts";
import type { MenuAccessControl } from "../../applications/access-control.ts";
import type { MenuReader } from "../../applications/get-menu/get-menu.ts";
import type { MenuView } from "../../applications/menu-support.ts";
import {
  Menu,
  type MenuRepository,
  type MenuSnapshot,
} from "../../domains/menu/menu.ts";

export const createMenuStore = (): InMemoryDocumentStore<MenuSnapshot> =>
  new InMemoryDocumentStore<MenuSnapshot>("Menu");

const findByRestaurant = (
  store: InMemoryDocumentStore<MenuSnapshot>,
  restaurantId: string,
) =>
  store.all().find((stored) => stored.document.restaurantId === restaurantId);

/** Enforces one menu per restaurant like a unique index on `restaurantId`. */
export class InMemoryMenuRepository implements MenuRepository {
  readonly #store: InMemoryDocumentStore<MenuSnapshot>;

  constructor(store: InMemoryDocumentStore<MenuSnapshot>) {
    this.#store = store;
  }

  async findByRestaurantId(restaurantId: string): Promise<Menu | undefined> {
    const stored = findByRestaurant(this.#store, restaurantId);
    return stored === undefined
      ? undefined
      : Menu.restore(stored.document, stored.version);
  }

  async save(menu: Menu): Promise<number> {
    const existing = findByRestaurant(this.#store, menu.restaurantId);
    if (existing !== undefined && existing.id !== menu.id) {
      throw new AlreadyExistsError("Menu", menu.restaurantId);
    }
    return this.#store.save(menu.id, menu.version, menu.toSnapshot());
  }
}

export class InMemoryMenuReader implements MenuReader {
  readonly #store: InMemoryDocumentStore<MenuSnapshot>;

  constructor(store: InMemoryDocumentStore<MenuSnapshot>) {
    this.#store = store;
  }

  async getByRestaurantId(restaurantId: string): Promise<MenuView | undefined> {
    const stored = findByRestaurant(this.#store, restaurantId);
    if (stored === undefined) {
      return undefined;
    }
    return {
      menuId: stored.document.id,
      restaurantId: stored.document.restaurantId,
      items: stored.document.items.map((item) => ({
        menuItemId: item.id,
        name: item.name,
        description: item.description,
        price: item.price,
        isAvailable: item.isAvailable,
      })),
    };
  }
}

/** Test authorization graph with explicit menu managers per restaurant. */
export class InMemoryMenuAccessControl implements MenuAccessControl {
  readonly #managers = new Set<string>();

  grantManager(subject: Subject, restaurantId: string): this {
    this.#managers.add(`${subject.type}:${subject.id}@${restaurantId}`);
    return this;
  }

  async canManageMenu(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean> {
    return this.#managers.has(`${subject.type}:${subject.id}@${restaurantId}`);
  }
}
