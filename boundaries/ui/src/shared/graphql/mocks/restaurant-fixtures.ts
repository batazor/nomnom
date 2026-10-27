import type {
  OrderEndReason,
  OrderMilestoneKind,
  OrderPriority,
  OrderStatus,
} from "../generated/graphql";
import type { MockTracking } from "./courier-tracks";

// Development-only data shaped like the public schema. It is not a source of
// business rules: the scenarios are fixed snapshots of the order lifecycle.

export const bffInfoFixture = Object.freeze({
  name: "nomnom BFF",
  version: "0.1.0-mock",
});

// Markets of GET /markets of the public gateway: by name, then by id.
export const marketFixtures = Object.freeze([
  Object.freeze({ id: "berlin", name: "Берлин", currency: "EUR" }),
  Object.freeze({ id: "hamburg", name: "Гамбург", currency: "EUR" }),
]);

export type MockMoney = { amount: string; currency: string };

export type MockMenuItem = {
  id: string;
  name: string;
  description: string | null;
  price: MockMoney;
  available: boolean;
};

export type MockWaitTime = { elevated: boolean; expectedWaitMinutes: number };

export type MockRestaurant = {
  id: string;
  name: string;
  acceptingOrders: boolean;
  menu: { items: MockMenuItem[] } | null;
  // Set by the catalog queries, null elsewhere like in the BFF.
  waitTime: MockWaitTime | null;
  // Fee of Premium Priority, null when the restaurant offers none (FR-652).
  premiumFee: MockMoney | null;
};

// A kitchen working at its usual pace.
const usualWait: MockWaitTime = { elevated: false, expectedWaitMinutes: 20 };

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
  priority: OrderPriority;
  // Fee fixed at checkout, null for a standard order.
  premiumFee: MockMoney | null;
  placedAt: string;
  milestones: MockOrderMilestone[];
  cancellation: { availableUntil: string } | null;
  // Deadlines named by the kitchen and the courier, null until named.
  expectedReadyAt: string | null;
  expectedPickupAt: string | null;
  expectedDeliveredAt: string | null;
  // Courier delivering the order, null until assigned (FR-626); absent on
  // the fixed scenarios, which have no courier.
  courier?: { name: string } | null;
  // Live tracking while the courier is assigned and the order is active,
  // null otherwise (FR-663); absent where the BFF leaves it null.
  tracking?: MockTracking | null;
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
  acceptingOrders: true,
  // The kitchen is slower than usual: the catalog shows the badge.
  waitTime: { elevated: true, expectedWaitMinutes: 42 },
  // Offers Premium Priority (FR-653); the other restaurants do not.
  premiumFee: { amount: "99.00", currency: "RUB" },
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
  acceptingOrders: false,
  waitTime: usualWait,
  premiumFee: null,
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

function item(
  id: string,
  name: string,
  amount: string,
  description: string | null = null,
): MockMenuItem {
  return { id, name, description, price: rub(amount), available: true };
}

// Open restaurants beyond the first page of the catalog, so that "show more"
// has something to load.
const moreRestaurants: MockRestaurant[] = [
  {
    id: "restaurant-north-dumplings",
    name: "Пельменная «Север»",
    acceptingOrders: true,
    waitTime: usualWait,
    premiumFee: null,
    menu: {
      items: [
        item(
          "menu-pelmeni",
          "Пельмени с говядиной",
          "390.00",
          "Сметана и укроп",
        ),
        item("menu-vareniki", "Вареники с вишней", "350.00"),
      ],
    },
  },
  {
    id: "restaurant-georgian-yard",
    name: "Грузинский дворик",
    acceptingOrders: true,
    waitTime: usualWait,
    premiumFee: null,
    menu: {
      items: [
        item("menu-khachapuri", "Хачапури по-аджарски", "520.00"),
        item("menu-khinkali", "Хинкали, 5 шт.", "450.00"),
      ],
    },
  },
  {
    id: "restaurant-sora-ramen",
    name: "Рамен-бар Сора",
    acceptingOrders: true,
    waitTime: usualWait,
    premiumFee: null,
    menu: {
      items: [item("menu-shoyu", "Сёю рамен", "640.00", "Курица, яйцо, нори")],
    },
  },
  {
    id: "restaurant-green-bowl",
    name: "Овощи и злаки",
    acceptingOrders: true,
    waitTime: usualWait,
    premiumFee: null,
    menu: {
      items: [item("menu-buddha-bowl", "Боул с киноа", "480.50")],
    },
  },
  {
    id: "restaurant-bridge-pizza",
    name: "Пицца у моста",
    acceptingOrders: true,
    waitTime: usualWait,
    premiumFee: null,
    menu: {
      items: [item("menu-margherita", "Маргарита", "560.00")],
    },
  },
  {
    id: "restaurant-corner-coffee",
    name: "Кофейня на углу",
    acceptingOrders: true,
    waitTime: usualWait,
    premiumFee: null,
    menu: null,
  },
];

export const restaurantFixtures: readonly MockRestaurant[] = [
  kitchenEight,
  nightBakery,
  ...moreRestaurants,
];

// Restaurants the mock user owns: openRestaurant and closeRestaurant succeed
// for them, any other existing restaurant answers PermissionDenied.
export const managedRestaurantIds: ReadonlySet<string> = new Set([
  kitchenEight.id,
  nightBakery.id,
]);

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

// Total with the premium fee fixed at checkout (FR-650).
export function withFee(total: MockMoney, fee: MockMoney): MockMoney {
  return fromMinor(toMinor(total) + toMinor(fee));
}
