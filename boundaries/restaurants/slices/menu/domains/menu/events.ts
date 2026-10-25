export type MenuEventBase = Readonly<{
  menuId: string;
  restaurantId: string;
  occurredAt: Date;
}>;

export type MenuCreated = MenuEventBase & Readonly<{ type: "MenuCreated" }>;
export type MenuItemAdded = MenuEventBase &
  Readonly<{ type: "MenuItemAdded"; menuItemId: string }>;
export type MenuItemUpdated = MenuEventBase &
  Readonly<{ type: "MenuItemUpdated"; menuItemId: string }>;
export type MenuItemAvailabilityChanged = MenuEventBase &
  Readonly<{
    type: "MenuItemAvailabilityChanged";
    menuItemId: string;
    isAvailable: boolean;
  }>;

export type MenuEvent =
  | MenuCreated
  | MenuItemAdded
  | MenuItemUpdated
  | MenuItemAvailabilityChanged;
