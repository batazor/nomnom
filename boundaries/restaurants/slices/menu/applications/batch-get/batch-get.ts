import type { MenuView } from "../menu-support/menu-support.ts";

export type BatchGetMenus = Readonly<{ restaurantIds: readonly string[] }>;

/** Query model port of the current menus of many restaurants. */
export interface MenuBatchReader {
  /** Menus of the distinct `restaurantIds` that have one, in no particular order. */
  getManyByRestaurantId(
    restaurantIds: readonly string[],
  ): Promise<readonly MenuView[]>;
}

const MAX_BATCH_SIZE = 100;

export class InvalidBatchError extends Error {
  readonly code = "invalid_batch";

  constructor(size: number) {
    super(`a batch must have 1..${MAX_BATCH_SIZE} ids, got ${size}`);
    this.name = "InvalidBatchError";
  }
}

/** The distinct ids of a batch; empty or more than 100 is InvalidBatchError. */
const batchIds = (ids: readonly string[]): readonly string[] => {
  if (ids.length === 0 || ids.length > MAX_BATCH_SIZE) {
    throw new InvalidBatchError(ids.length);
  }
  return [...new Set(ids)];
};

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
