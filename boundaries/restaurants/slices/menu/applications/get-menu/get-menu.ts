import { NotFoundError } from "../../../../pkg/usecase/errors.ts";
import type { MenuView } from "../menu-support.ts";

export type GetMenu = Readonly<{ restaurantId: string }>;

/** Query model port of the current restaurant menu. */
export interface MenuReader {
  getByRestaurantId(restaurantId: string): Promise<MenuView | undefined>;
}

/**
 * Returns the current menu: items, prices and availability. The result is not
 * a snapshot of a placed order. Public data needs no authorization.
 */
export class GetMenuHandler {
  readonly #reader: MenuReader;

  constructor(dependencies: { reader: MenuReader }) {
    this.#reader = dependencies.reader;
  }

  async execute(query: GetMenu): Promise<MenuView> {
    const menu = await this.#reader.getByRestaurantId(query.restaurantId);
    if (menu === undefined) {
      throw new NotFoundError("Menu", query.restaurantId);
    }
    return menu;
  }
}
