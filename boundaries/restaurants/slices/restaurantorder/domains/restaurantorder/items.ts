import {
  InvalidRestaurantOrderError,
  InvalidRestaurantOrderReason,
} from "./errors.ts";
import { InvalidQuantityError, Quantity } from "./vo/quantity.ts";

/** Position of a Restaurant Order in kitchen work; it carries no price. */
export type PreparationItem = Readonly<{
  menuItemId: string;
  quantity: Quantity;
  /** Opaque ids of selected options; the Modifier model is not agreed yet. */
  options: readonly string[];
}>;

export type PreparationItemInput = Readonly<{
  menuItemId: string;
  quantity: number;
  options?: readonly string[];
}>;

const itemKey = (item: {
  menuItemId: string;
  options: readonly string[];
}): string => JSON.stringify([item.menuItemId, item.options]);

export const toPreparationItems = (
  inputs: readonly PreparationItemInput[],
): PreparationItem[] => {
  if (inputs.length === 0) {
    throw new InvalidRestaurantOrderError(
      InvalidRestaurantOrderReason.EmptyItems,
    );
  }
  const keys = new Set<string>();
  return inputs.map((input) => {
    const menuItemId = input.menuItemId.trim();
    if (menuItemId.length === 0) {
      throw new InvalidRestaurantOrderError(
        InvalidRestaurantOrderReason.EmptyMenuItemId,
      );
    }
    let quantity: Quantity;
    try {
      quantity = Quantity.of(input.quantity);
    } catch (error) {
      if (error instanceof InvalidQuantityError) {
        throw new InvalidRestaurantOrderError(
          InvalidRestaurantOrderReason.InvalidQuantity,
        );
      }
      throw error;
    }
    const options = [...new Set(input.options ?? [])].sort();
    const key = itemKey({ menuItemId, options });
    if (keys.has(key)) {
      throw new InvalidRestaurantOrderError(
        InvalidRestaurantOrderReason.DuplicateItem,
      );
    }
    keys.add(key);
    return { menuItemId, quantity, options };
  });
};

export const optionalText = (text: string | undefined): string | undefined => {
  const normalized = text?.trim();
  return normalized === undefined || normalized.length === 0
    ? undefined
    : normalized;
};

export const compositionKey = (items: readonly PreparationItem[]): string =>
  JSON.stringify(
    items
      .map((item) => [itemKey(item), item.quantity.value] as const)
      .sort(([left], [right]) => (left < right ? -1 : left > right ? 1 : 0)),
  );
