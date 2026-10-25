import type { CustomerJourneyFixture, JourneyState } from "./model";

export const customerJourneyFixture = {
  restaurant: {
    id: "restaurant-fixture-kitchen-8",
    nameKey: "journey.restaurant.name",
    cuisineKey: "journey.restaurant.cuisine",
    etaKey: "journey.restaurant.eta",
  },
  menu: [
    {
      id: "tom-yum",
      nameKey: "journey.item.tomYum.name",
      descriptionKey: "journey.item.tomYum.description",
      priceMinor: 79000,
    },
    {
      id: "mushroom-bao",
      nameKey: "journey.item.bao.name",
      descriptionKey: "journey.item.bao.description",
      priceMinor: 39000,
    },
    {
      id: "mango-rice",
      nameKey: "journey.item.mango.name",
      descriptionKey: "journey.item.mango.description",
      priceMinor: 44000,
    },
  ],
  checkout: {
    addressKey: "journey.checkout.addressValue",
    paymentKey: "journey.checkout.paymentValue",
    deliveryMinor: 14900,
    serviceMinor: 7900,
  },
  courier: {
    id: "courier-fixture-alexey",
    nameKey: "journey.courier.name",
    transportKey: "journey.courier.transport",
    initials: "А",
  },
  deliveryStages: [
    {
      id: "received",
      labelKey: "journey.stage.received.label",
      detailKey: "journey.stage.received.detail",
    },
    {
      id: "preparing",
      labelKey: "journey.stage.preparing.label",
      detailKey: "journey.stage.preparing.detail",
    },
    {
      id: "courier-assigned",
      labelKey: "journey.stage.courierAssigned.label",
      detailKey: "journey.stage.courierAssigned.detail",
    },
    {
      id: "picked-up",
      labelKey: "journey.stage.pickedUp.label",
      detailKey: "journey.stage.pickedUp.detail",
    },
    {
      id: "arriving",
      labelKey: "journey.stage.arriving.label",
      detailKey: "journey.stage.arriving.detail",
    },
  ],
} as const satisfies CustomerJourneyFixture;

export const initialJourneyState = {
  quantities: {
    "tom-yum": 1,
    "mushroom-bao": 2,
    "mango-rice": 0,
  },
  deliveryStage: "courier-assigned",
} as const satisfies JourneyState;
