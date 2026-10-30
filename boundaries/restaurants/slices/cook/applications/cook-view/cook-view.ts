import type { Cook } from "../../domains/cook/cook.ts";

/** Representation of one cook returned by the Cook Pool use cases. */
export type CookView = Readonly<{
  cookId: string;
  restaurantId: string;
  name: string;
  onLine: boolean;
  /** Restaurant Order in Active Preparation held by the cook. */
  currentRestaurantOrderId: string | undefined;
}>;

export type CookChangeResult = CookView & Readonly<{ changed: boolean }>;

export const toCookView = (
  cook: Cook,
  currentRestaurantOrderId: string | undefined,
): CookView => ({
  cookId: cook.id,
  restaurantId: cook.restaurantId,
  name: cook.name,
  onLine: cook.onLine,
  currentRestaurantOrderId,
});
