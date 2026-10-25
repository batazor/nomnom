import type { Db } from "mongodb";

import { AlreadyExistsError } from "../../../../pkg/application/errors.ts";
import type { MongoUnitOfWork } from "../../../../pkg/infrastructure/mongodb/unit-of-work.ts";
import {
  VersionedCollection,
  type VersionedDocument,
} from "../../../../pkg/infrastructure/mongodb/versioned-collection.ts";
import type { MenuReader } from "../../applications/get-menu/get-menu.ts";
import type { MenuView } from "../../applications/menu-support.ts";
import {
  Menu,
  type MenuRepository,
  type MenuSnapshot,
} from "../../domains/menu/menu.ts";

export const MENUS_COLLECTION = "menus";
export const MENU_SCHEMA_VERSION = 1;

/**
 * One document per Menu aggregate with its Menu Items embedded: they are read
 * and changed together and the item count is bounded by one restaurant menu.
 * At most one Menu per restaurant (unique `restaurantId`). Money is stored as
 * a decimal string with currency, never as a binary float.
 */
export type MenuDocument = VersionedDocument & {
  restaurantId: string;
  items: {
    id: string;
    name: string;
    description?: string;
    price: { amount: string; currency: string };
    isAvailable: boolean;
  }[];
};

const toSnapshot = (document: MenuDocument): MenuSnapshot => ({
  id: document._id,
  restaurantId: document.restaurantId,
  items: document.items.map((item) => ({
    id: item.id,
    name: item.name,
    description: item.description ?? undefined,
    price: { amount: item.price.amount, currency: item.price.currency },
    isAvailable: item.isAvailable,
  })),
});

export const createMenuCollection = (
  db: Db,
  unitOfWork: MongoUnitOfWork,
): VersionedCollection<MenuDocument> =>
  new VersionedCollection<MenuDocument>({
    db,
    name: MENUS_COLLECTION,
    aggregateType: "Menu",
    unitOfWork,
    duplicateKey: (keyPattern, document) =>
      "restaurantId" in keyPattern
        ? new AlreadyExistsError("Menu", document.restaurantId)
        : undefined,
  });

export class MongoMenuRepository implements MenuRepository {
  readonly #collection: VersionedCollection<MenuDocument>;

  constructor(collection: VersionedCollection<MenuDocument>) {
    this.#collection = collection;
  }

  async findByRestaurantId(restaurantId: string): Promise<Menu | undefined> {
    const document = await this.#collection.findOne({ restaurantId });
    return document === undefined
      ? undefined
      : Menu.restore(toSnapshot(document), document.version);
  }

  async save(menu: Menu): Promise<number> {
    const snapshot = menu.toSnapshot();
    return this.#collection.save(menu.id, menu.version, {
      schemaVersion: MENU_SCHEMA_VERSION,
      restaurantId: snapshot.restaurantId,
      items: snapshot.items.map((item) => ({
        id: item.id,
        name: item.name,
        description: item.description,
        price: { amount: item.price.amount, currency: item.price.currency },
        isAvailable: item.isAvailable,
      })),
    });
  }
}

export class MongoMenuReader implements MenuReader {
  readonly #collection: VersionedCollection<MenuDocument>;

  constructor(collection: VersionedCollection<MenuDocument>) {
    this.#collection = collection;
  }

  async getByRestaurantId(restaurantId: string): Promise<MenuView | undefined> {
    const document = await this.#collection.findOne({ restaurantId });
    if (document === undefined) return undefined;
    const snapshot = toSnapshot(document);
    return {
      menuId: snapshot.id,
      restaurantId: snapshot.restaurantId,
      items: snapshot.items.map((item) => ({
        menuItemId: item.id,
        name: item.name,
        description: item.description,
        price: item.price,
        isAvailable: item.isAvailable,
      })),
    };
  }
}
