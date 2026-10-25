export type RestaurantEventBase = Readonly<{
  restaurantId: string;
  occurredAt: Date;
}>;

export type RestaurantRegistered = RestaurantEventBase &
  Readonly<{
    type: "RestaurantRegistered";
    name: string;
    ownerId: string;
    address: string;
  }>;
export type RestaurantRenamed = RestaurantEventBase &
  Readonly<{ type: "RestaurantRenamed"; name: string }>;
export type RestaurantAddressChanged = RestaurantEventBase &
  Readonly<{ type: "RestaurantAddressChanged"; address: string }>;
export type RestaurantOpened = RestaurantEventBase &
  Readonly<{ type: "RestaurantOpened" }>;
export type RestaurantClosed = RestaurantEventBase &
  Readonly<{ type: "RestaurantClosed" }>;

export type RestaurantEvent =
  | RestaurantRegistered
  | RestaurantRenamed
  | RestaurantAddressChanged
  | RestaurantOpened
  | RestaurantClosed;
