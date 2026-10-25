import type {
  OrderEndReason,
  OrderMilestoneKind,
  OrderStatus,
} from "../generated/graphql";

// Development-only data shaped like the public schema. It is not a source of
// business rules: the scenarios are fixed snapshots of the order lifecycle.

export const bffInfoFixture = Object.freeze({
  name: "BiteRoute BFF",
  version: "0.1.0-mock",
});

export type MockMoney = { amount: string; currency: string };

export type MockMenuItem = {
  id: string;
  name: string;
  description: string | null;
  price: MockMoney;
  available: boolean;
};

export type MockRestaurant = {
  id: string;
  name: string;
  address: string;
  acceptingOrders: boolean;
  menu: { items: MockMenuItem[] } | null;
};

export type MockOrderItem = {
  menuItem: { id: string; name: string };
  quantity: number;
  unitPrice: MockMoney;
  lineTotal: MockMoney;
};

export type MockOrderMilestone = {
  kind: OrderMilestoneKind;
  occurredAt: string;
};

export type MockOrder = {
  id: string;
  status: OrderStatus;
  endReason: OrderEndReason | null;
  restaurant: MockRestaurant;
  items: MockOrderItem[];
  total: MockMoney;
  deliveryAddress: string;
  placedAt: string;
  milestones: MockOrderMilestone[];
  cancellation: { availableUntil: string } | null;
};

const currency = "RUB";

export function rub(amount: string): MockMoney {
  return { amount, currency };
}

export function toMinor(money: MockMoney): number {
  const [units, fraction = ""] = money.amount.split(".");
  return Number(units) * 100 + Number(fraction.padEnd(2, "0").slice(0, 2));
}

export function fromMinor(minor: number): MockMoney {
  const units = Math.trunc(minor / 100);
  const fraction = String(minor % 100).padStart(2, "0");
  return rub(`${units}.${fraction}`);
}

export const kitchenEight: MockRestaurant = {
  id: "restaurant-kitchen-8",
  name: "Кухня №8",
  address: "ул. Садовая, 8",
  acceptingOrders: true,
  menu: {
    items: [
      {
        id: "menu-tom-yum",
        name: "Том ям с креветками",
        description: "Кокосовый бульон, грибы, томаты и жасминовый рис",
        price: rub("690.00"),
        available: true,
      },
      {
        id: "menu-bao",
        name: "Бао с грибами",
        description: "Две паровые булочки, шиитаке и кунжутный соус",
        price: rub("420.00"),
        available: true,
      },
      {
        id: "menu-mango",
        name: "Манго и клейкий рис",
        description: "Спелое манго, кокосовый крем и хрустящий маш",
        price: rub("440.00"),
        available: true,
      },
      {
        id: "menu-gyoza",
        name: "Гёдза с уткой",
        description: null,
        price: rub("510.00"),
        available: false,
      },
    ],
  },
};

export const nightBakery: MockRestaurant = {
  id: "restaurant-night-bakery",
  name: "Ночная пекарня",
  address: "пр. Мира, 31",
  acceptingOrders: false,
  menu: {
    items: [
      {
        id: "menu-croissant",
        name: "Круассан с миндалём",
        description: "Сливочное тесто и миндальный крем",
        price: rub("260.00"),
        available: true,
      },
    ],
  },
};

export const restaurantFixtures: readonly MockRestaurant[] = [
  kitchenEight,
  nightBakery,
];

export const deliveryAddressFixture = "ул. Лесная, 12, кв. 47";

export function orderLine(item: MockMenuItem, quantity: number): MockOrderItem {
  return {
    menuItem: { id: item.id, name: item.name },
    quantity,
    unitPrice: item.price,
    lineTotal: fromMinor(toMinor(item.price) * quantity),
  };
}

export function orderTotal(items: readonly MockOrderItem[]): MockMoney {
  return fromMinor(
    items.reduce((sum, item) => sum + toMinor(item.lineTotal), 0),
  );
}

const minute = 60_000;

function at(now: Date, minutesFromNow: number): string {
  return new Date(now.getTime() + minutesFromNow * minute).toISOString();
}

const menu = kitchenEight.menu?.items ?? [];
const defaultItems = [orderLine(menu[0], 1), orderLine(menu[1], 2)];

type ScenarioSpec = {
  id: string;
  status: OrderStatus;
  endReason?: OrderEndReason;
  placedMinutesAgo: number;
  // Minutes after placing for each milestone after PLACED.
  milestones?: ReadonlyArray<readonly [OrderMilestoneKind, number]>;
  cancellableForMinutes?: number;
};

function buildOrder(spec: ScenarioSpec, now: Date): MockOrder {
  const placed = -spec.placedMinutesAgo;
  return {
    id: spec.id,
    status: spec.status,
    endReason: spec.endReason ?? null,
    restaurant: kitchenEight,
    items: defaultItems,
    total: orderTotal(defaultItems),
    deliveryAddress: deliveryAddressFixture,
    placedAt: at(now, placed),
    milestones: [
      { kind: "PLACED", occurredAt: at(now, placed) },
      ...(spec.milestones ?? []).map(([kind, offset]) => ({
        kind,
        occurredAt: at(now, placed + offset),
      })),
    ],
    cancellation:
      spec.cancellableForMinutes === undefined
        ? null
        : { availableUntil: at(now, spec.cancellableForMinutes) },
  };
}

const preparing = [
  ["CONFIRMED", 1],
  ["PREPARATION_STARTED", 4],
  ["COURIER_ASSIGNED", 9],
] as const;

const pickedUp = [
  ...preparing,
  ["READY_FOR_PICKUP", 18],
  ["PICKED_UP", 21],
] as const;

const scenarios: readonly ScenarioSpec[] = [
  {
    id: "order-received",
    status: "RECEIVED",
    placedMinutesAgo: 1,
    cancellableForMinutes: 4,
  },
  {
    id: "order-preparing",
    status: "PREPARING",
    placedMinutesAgo: 12,
    milestones: preparing,
  },
  {
    id: "order-picked-up",
    status: "PICKED_UP",
    placedMinutesAgo: 25,
    milestones: pickedUp,
  },
  {
    id: "order-delivered",
    status: "DELIVERED",
    placedMinutesAgo: 90,
    milestones: [...pickedUp, ["DELIVERED", 34]],
  },
  {
    id: "order-cancelled",
    status: "CANCELLED",
    endReason: "CUSTOMER_CANCELLED",
    placedMinutesAgo: 180,
    milestones: [["ENDED", 2]],
  },
  {
    id: "order-restaurant-rejected",
    status: "FAILED",
    endReason: "RESTAURANT_REJECTED",
    placedMinutesAgo: 240,
    milestones: [["ENDED", 3]],
  },
  {
    id: "order-delivery-failed",
    status: "FAILED",
    endReason: "DELIVERY_FAILED",
    placedMinutesAgo: 300,
    milestones: [...pickedUp, ["ENDED", 52]],
  },
];

export function createOrderScenarios(now: Date): MockOrder[] {
  return scenarios.map((spec) => buildOrder(spec, now));
}

export const liveOrderId = "order-live";

// order-live walks the happy path one step per request.
const liveSteps: readonly ScenarioSpec[] = [
  {
    id: liveOrderId,
    status: "RECEIVED",
    placedMinutesAgo: 0,
    cancellableForMinutes: 5,
  },
  {
    id: liveOrderId,
    status: "CONFIRMED",
    placedMinutesAgo: 0,
    milestones: preparing.slice(0, 1),
  },
  {
    id: liveOrderId,
    status: "PREPARING",
    placedMinutesAgo: 0,
    milestones: preparing,
  },
  {
    id: liveOrderId,
    status: "READY_FOR_PICKUP",
    placedMinutesAgo: 0,
    milestones: pickedUp.slice(0, 4),
  },
  {
    id: liveOrderId,
    status: "PICKED_UP",
    placedMinutesAgo: 0,
    milestones: pickedUp,
  },
  {
    id: liveOrderId,
    status: "DELIVERED",
    placedMinutesAgo: 0,
    milestones: [...pickedUp, ["DELIVERED", 34]],
  },
];

export const liveOrderStepCount = liveSteps.length;

export function liveOrderAt(step: number, startedAt: Date): MockOrder {
  return buildOrder(liveSteps[Math.min(step, liveSteps.length - 1)], startedAt);
}
