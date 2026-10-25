import type { MessageKey } from "../../shared/i18n/messages";

export type MenuItemId = "tom-yum" | "mushroom-bao" | "mango-rice";
export type DeliveryStageId =
  | "received"
  | "preparing"
  | "courier-assigned"
  | "picked-up"
  | "arriving";

export type MenuItemFixture = Readonly<{
  id: MenuItemId;
  nameKey: MessageKey;
  descriptionKey: MessageKey;
  priceMinor: number;
}>;

export type DeliveryStageFixture = Readonly<{
  id: DeliveryStageId;
  labelKey: MessageKey;
  detailKey: MessageKey;
}>;

export type CustomerJourneyFixture = Readonly<{
  restaurant: Readonly<{
    id: "restaurant-fixture-kitchen-8";
    nameKey: MessageKey;
    cuisineKey: MessageKey;
    etaKey: MessageKey;
  }>;
  menu: readonly MenuItemFixture[];
  checkout: Readonly<{
    addressKey: MessageKey;
    paymentKey: MessageKey;
    deliveryMinor: number;
    serviceMinor: number;
  }>;
  courier: Readonly<{
    id: "courier-fixture-alexey";
    nameKey: MessageKey;
    transportKey: MessageKey;
    initials: "А";
  }>;
  deliveryStages: readonly DeliveryStageFixture[];
}>;

export type JourneyState = Readonly<{
  quantities: Readonly<Record<MenuItemId, number>>;
  deliveryStage: DeliveryStageId;
}>;

export type JourneyAction =
  | Readonly<{ type: "increment"; itemId: MenuItemId }>
  | Readonly<{ type: "decrement"; itemId: MenuItemId }>
  | Readonly<{ type: "set-delivery-stage"; stageId: DeliveryStageId }>;

export function journeyReducer(
  state: JourneyState,
  action: JourneyAction,
): JourneyState {
  if (action.type === "set-delivery-stage") {
    return { ...state, deliveryStage: action.stageId };
  }

  const currentQuantity = state.quantities[action.itemId];
  const nextQuantity =
    action.type === "increment"
      ? currentQuantity + 1
      : Math.max(0, currentQuantity - 1);

  return {
    ...state,
    quantities: {
      ...state.quantities,
      [action.itemId]: nextQuantity,
    },
  };
}

export function deriveSubtotalMinor(
  menu: readonly MenuItemFixture[],
  quantities: JourneyState["quantities"],
): number {
  return menu.reduce(
    (subtotal, item) => subtotal + item.priceMinor * quantities[item.id],
    0,
  );
}
