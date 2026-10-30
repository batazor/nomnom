import { MarketId } from "../../../restaurant/domains/restaurant/vo/market-id.ts";
import {
  Money,
  type MoneyValue,
} from "../../../restaurant/domains/restaurant/vo/money.ts";
import { InvalidMenuError, InvalidMenuReason } from "./errors.ts";
import type {
  MenuCreated,
  MenuEvent,
  MenuEventBase,
  MenuItemAdded,
  MenuItemAvailabilityChanged,
  MenuItemUpdated,
} from "./events.ts";

export * from "./availability.ts";
export * from "./errors.ts";
export type {
  MenuCreated,
  MenuEvent,
  MenuItemAdded,
  MenuItemAvailabilityChanged,
  MenuItemUpdated,
} from "./events.ts";

/** Menu Item: entity of {@link Menu}, a dish with its current price and availability. */
export type MenuItem = Readonly<{
  id: string;
  name: string;
  description: string | undefined;
  price: Money;
  isAvailable: boolean;
}>;

export type MenuItemSnapshot = Readonly<{
  id: string;
  name: string;
  description: string | undefined;
  price: MoneyValue;
  isAvailable: boolean;
}>;

export type MenuSnapshot = Readonly<{
  id: string;
  restaurantId: string;
  /** Market of the restaurant (ADR 41), immutable. */
  marketId: string;
  items: readonly MenuItemSnapshot[];
}>;

export type MenuItemChanges = Readonly<{
  name?: string;
  /** `null` removes the description. */
  description?: string | null;
  price?: Money;
  isAvailable?: boolean;
}>;

const itemName = (name: string): string => {
  const normalized = name.trim();
  if (normalized.length === 0) {
    throw new InvalidMenuError(InvalidMenuReason.EmptyItemName);
  }
  return normalized;
};

const itemDescription = (
  description: string | null | undefined,
): string | undefined => {
  const normalized = description?.trim();
  return normalized === undefined || normalized.length === 0
    ? undefined
    : normalized;
};

/**
 * Aggregate root of the current offer of one restaurant. A restaurant has at
 * most one Menu; Menu Item ids are unique inside it. Changes never affect
 * snapshots of already placed orders, which live in `orders`.
 */
export class Menu {
  readonly #id: string;
  readonly #restaurantId: string;
  readonly #marketId: MarketId;
  readonly #version: number;
  #items: MenuItem[];
  #events: MenuEvent[] = [];

  private constructor(
    id: string,
    restaurantId: string,
    marketId: MarketId,
    items: MenuItem[],
    version: number,
  ) {
    this.#id = id;
    this.#restaurantId = restaurantId;
    this.#marketId = marketId;
    this.#items = items;
    this.#version = version;
  }

  static create(input: {
    id: string;
    restaurantId: string;
    /** Market of the restaurant the menu belongs to (ADR 41). */
    marketId: string;
    createdAt: Date;
  }): Menu {
    const menu = new Menu(
      input.id,
      input.restaurantId,
      MarketId.of(input.marketId),
      [],
      0,
    );
    menu.#record({ type: "MenuCreated" }, input.createdAt);
    return menu;
  }

  static restore(snapshot: MenuSnapshot, version: number): Menu {
    return new Menu(
      snapshot.id,
      snapshot.restaurantId,
      MarketId.of(snapshot.marketId),
      snapshot.items.map((item) => ({
        ...item,
        price: Money.from(item.price),
      })),
      version,
    );
  }

  get id(): string {
    return this.#id;
  }

  get restaurantId(): string {
    return this.#restaurantId;
  }

  get marketId(): MarketId {
    return this.#marketId;
  }

  get version(): number {
    return this.#version;
  }

  get items(): readonly MenuItem[] {
    return this.#items;
  }

  findItem(menuItemId: string): MenuItem | undefined {
    return this.#items.find((item) => item.id === menuItemId);
  }

  addItem(input: {
    id: string;
    name: string;
    description?: string;
    price: Money;
    isAvailable: boolean;
    addedAt: Date;
  }): MenuItem {
    if (this.findItem(input.id) !== undefined) {
      throw new InvalidMenuError(InvalidMenuReason.DuplicateMenuItem);
    }
    const item: MenuItem = {
      id: input.id,
      name: itemName(input.name),
      description: itemDescription(input.description),
      price: input.price,
      isAvailable: input.isAvailable,
    };
    this.#items = [...this.#items, item];
    this.#record({ type: "MenuItemAdded", menuItemId: item.id }, input.addedAt);
    return item;
  }

  /**
   * Applies the given changes; returns `false` when nothing changed. A change
   * of availability records `MenuItemAvailabilityChanged`.
   */
  updateItem(
    menuItemId: string,
    changes: MenuItemChanges,
    updatedAt: Date,
  ): boolean {
    const current = this.#requireItem(menuItemId);
    const next: MenuItem = {
      id: current.id,
      name: changes.name === undefined ? current.name : itemName(changes.name),
      description:
        changes.description === undefined
          ? current.description
          : itemDescription(changes.description),
      price: changes.price ?? current.price,
      isAvailable: changes.isAvailable ?? current.isAvailable,
    };
    const detailsChanged =
      next.name !== current.name ||
      next.description !== current.description ||
      !next.price.equals(current.price);
    const availabilityChanged = next.isAvailable !== current.isAvailable;
    if (!detailsChanged && !availabilityChanged) {
      return false;
    }
    this.#replace(next);
    if (detailsChanged) {
      this.#record({ type: "MenuItemUpdated", menuItemId }, updatedAt);
    }
    if (availabilityChanged) {
      this.#recordAvailability(next, updatedAt);
    }
    return true;
  }

  /** Changes only availability; returns `false` when the value is already current. */
  setItemAvailability(
    menuItemId: string,
    isAvailable: boolean,
    changedAt: Date,
  ): boolean {
    const current = this.#requireItem(menuItemId);
    if (current.isAvailable === isAvailable) {
      return false;
    }
    const next = { ...current, isAvailable };
    this.#replace(next);
    this.#recordAvailability(next, changedAt);
    return true;
  }

  toSnapshot(): MenuSnapshot {
    return {
      id: this.#id,
      restaurantId: this.#restaurantId,
      marketId: this.#marketId.value,
      items: this.#items.map((item) => ({
        ...item,
        price: item.price.toValue(),
      })),
    };
  }

  pullEvents(): MenuEvent[] {
    const events = this.#events;
    this.#events = [];
    return events;
  }

  #requireItem(menuItemId: string): MenuItem {
    const item = this.findItem(menuItemId);
    if (item === undefined) {
      throw new InvalidMenuError(InvalidMenuReason.UnknownMenuItem);
    }
    return item;
  }

  #replace(next: MenuItem): void {
    this.#items = this.#items.map((item) =>
      item.id === next.id ? next : item,
    );
  }

  #recordAvailability(item: MenuItem, occurredAt: Date): void {
    this.#record(
      {
        type: "MenuItemAvailabilityChanged",
        menuItemId: item.id,
        isAvailable: item.isAvailable,
      },
      occurredAt,
    );
  }

  #record(
    event:
      | Omit<MenuCreated, keyof MenuEventBase>
      | Omit<MenuItemAdded, keyof MenuEventBase>
      | Omit<MenuItemUpdated, keyof MenuEventBase>
      | Omit<MenuItemAvailabilityChanged, keyof MenuEventBase>,
    occurredAt: Date,
  ): void {
    this.#events.push({
      ...event,
      menuId: this.#id,
      restaurantId: this.#restaurantId,
      marketId: this.#marketId.value,
      occurredAt,
    } as MenuEvent);
  }
}

/**
 * Repository contract of {@link Menu}. At most one Menu exists per restaurant;
 * `save` uses optimistic concurrency and returns the committed version.
 */
export interface MenuRepository {
  findByRestaurantId(restaurantId: string): Promise<Menu | undefined>;
  save(menu: Menu): Promise<number>;
}
