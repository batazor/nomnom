import { MenuItemAvailable } from "../../../menu/domains/menu/menu.ts";
import { RestaurantAcceptingOrders } from "../../../restaurant/domains/restaurant/restaurant.ts";
import type { AcceptanceEligibility, GuardFailureReason } from "./fsm.ts";

export const AcceptanceReasonCode = {
  RestaurantClosed: "restaurant_closed",
  MenuItemNotFound: "menu_item_not_found",
  MenuItemConditionsMissing: "menu_item_conditions_missing",
} as const;

/** Snapshot of out-of-state conditions supplied by the application at `now`. */
export type AcceptanceConditions = Readonly<{
  restaurant: Readonly<{
    opensAt: Date;
    closesAt: Date;
    currentOrders: number;
    maximumOrders: number;
    scheduleException: boolean;
  }>;
  menuItems: Readonly<
    Record<
      string,
      Readonly<{
        availableFrom: Date;
        availableUntil: Date;
        remainingCapacity: number;
      }>
    >
  >;
}>;

export type AcceptanceInput = Readonly<{
  now: Date;
  restaurant: Readonly<{ isOpen: boolean }>;
  menu:
    | Readonly<{
        findItem(
          menuItemId: string,
        ): Readonly<{ isAvailable: boolean }> | undefined;
      }>
    | undefined;
  items: readonly Readonly<{ menuItemId: string; quantity: number }>[];
  conditions: AcceptanceConditions;
}>;

/**
 * Domain service over already loaded Restaurant, Menu and the condition
 * snapshot: an open restaurant accepting orders and every item available.
 * It collects every reason instead of stopping at the first one.
 */
export const evaluateOrderAcceptance = (
  input: AcceptanceInput,
): AcceptanceEligibility => {
  const reasons: GuardFailureReason[] = [];
  if (!input.restaurant.isOpen) {
    reasons.push({ code: AcceptanceReasonCode.RestaurantClosed });
  }
  const restaurantResult = RestaurantAcceptingOrders({
    now: input.now,
    ...input.conditions.restaurant,
  });
  for (const code of restaurantResult.reasons) {
    reasons.push({ code });
  }
  // Lines of one Menu Item with different options share its capacity.
  const requested = new Map<string, number>();
  for (const item of input.items) {
    requested.set(
      item.menuItemId,
      (requested.get(item.menuItemId) ?? 0) + item.quantity,
    );
  }
  for (const [menuItemId, quantity] of requested) {
    const item = { menuItemId, quantity };
    const menuItem = input.menu?.findItem(item.menuItemId);
    if (menuItem === undefined) {
      reasons.push({
        code: AcceptanceReasonCode.MenuItemNotFound,
        menuItemId: item.menuItemId,
      });
      continue;
    }
    const offer = input.conditions.menuItems[item.menuItemId];
    if (offer === undefined) {
      reasons.push({
        code: AcceptanceReasonCode.MenuItemConditionsMissing,
        menuItemId: item.menuItemId,
      });
      continue;
    }
    const itemResult = MenuItemAvailable({
      now: input.now,
      ...offer,
      requestedQuantity: item.quantity,
      temporarilyUnavailable: !menuItem.isAvailable,
    });
    for (const code of itemResult.reasons) {
      reasons.push({ code, menuItemId: item.menuItemId });
    }
  }
  return { satisfied: reasons.length === 0, reasons };
};
