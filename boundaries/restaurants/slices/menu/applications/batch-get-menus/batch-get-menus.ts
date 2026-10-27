import { batchIds } from "../../../../pkg/application/batch.ts";
import type { MenuView } from "../menu-support.ts";

export type BatchGetMenus = Readonly<{ restaurantIds: readonly string[] }>;

/** Query model port of the current menus of many restaurants. */
export interface MenuBatchReader {
  /** Menus of the distinct `restaurantIds` that have one, in no particular order. */
  getManyByRestaurantId(
    restaurantIds: readonly string[],
  ): Promise<readonly MenuView[]>;
}

/**
 * Returns the current menus of up to 100 restaurants for a page of the caller
 * (ADR 7). A restaurant without a menu is absent from the result; public data
 * needs no authorization.
 */
export class BatchGetMenusHandler {
  readonly #reader: MenuBatchReader;

  constructor(dependencies: { reader: MenuBatchReader }) {
    this.#reader = dependencies.reader;
  }

  async execute(query: BatchGetMenus): Promise<readonly MenuView[]> {
    return this.#reader.getManyByRestaurantId(batchIds(query.restaurantIds));
  }
}
