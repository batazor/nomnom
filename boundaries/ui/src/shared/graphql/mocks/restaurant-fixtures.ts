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
