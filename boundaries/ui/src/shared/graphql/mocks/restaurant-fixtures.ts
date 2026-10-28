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
  Object.freeze({
    id: "berlin",
    name: "Берлин",
    currency: "EUR",
    timeZone: "Europe/Berlin",
  }),
  Object.freeze({
    id: "hamburg",
    name: "Гамбург",
    currency: "EUR",
    timeZone: "Europe/Berlin",
  }),
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

export type MockOpeningInterval = {
  dayOfWeek: number;
  opens: string;
  closes: string;
};

export type MockDeliveryWindow = {
  start: string;
  end: string;
  available: boolean;
};

export type MockRestaurant = {
  id: string;
  name: string;
  acceptingOrders: boolean;
  menu: { items: MockMenuItem[] } | null;
  waitTime: MockWaitTime;
  // Fee of Premium Priority, null when the restaurant offers none (FR-652).
  premiumFee: MockMoney | null;
  // IANA zone of the restaurant, the one of its Market (FR-201).
  timeZone: string;
  // Weekly opening hours on the wall clock of the restaurant (FR-696).
  openingHours: MockOpeningInterval[];
  // Start of the next period while opened but outside the hours (FR-204).
  nextOpeningAt: string | null;
  // Windows of scheduled orders; the store lists them at the time of the
  // read, the fixtures have none (FR-685).
  scheduledSlots: MockDeliveryWindow[];
};

// Restaurant outside the catalog: of an order, a kitchen or its owner (FR-670).
export type MockRestaurantSummary = Pick<
  MockRestaurant,
  "id" | "name" | "acceptingOrders"
>;

export function restaurantSummary({
  id,
  name,
  acceptingOrders,
}: MockRestaurant): MockRestaurantSummary {
  return { id, name, acceptingOrders };
}

// A kitchen working at its usual pace.
const usualWait: MockWaitTime = { elevated: false, expectedWaitMinutes: 20 };

// Every mock restaurant is in Market berlin.
const berlinTime = "Europe/Berlin";

// The same interval every day of the week.
function dailyHours(opens: string, closes: string): MockOpeningInterval[] {
  return [1, 2, 3, 4, 5, 6, 7].map((dayOfWeek) => ({
    dayOfWeek,
    opens,
    closes,
  }));
}

// The schedule fields of a restaurant inside its hours.
const daily = {
  timeZone: berlinTime,
  openingHours: dailyHours("10:00", "23:00"),
  nextOpeningAt: null,
  scheduledSlots: [],
} satisfies Partial<MockRestaurant>;

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
  restaurant: MockRestaurantSummary;
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
  // Delivery window of a scheduled order (FR-687); absent or null for ASAP.
  deliveryWindow?: { start: string; end: string } | null;
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
  ...daily,
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
  timeZone: berlinTime,
  // Opens at night: Friday and Saturday into the next morning.
  openingHours: [
    { dayOfWeek: 5, opens: "22:00", closes: "06:00" },
    { dayOfWeek: 6, opens: "22:00", closes: "06:00" },
  ],
  nextOpeningAt: null,
  scheduledSlots: [],
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
    ...daily,
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
    ...daily,
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
    ...daily,
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
    ...daily,
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
    ...daily,
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
    ...daily,
    menu: null,
  },
];

// Opened by its owner but before its hours (FR-702): the store names the next
// opening on the clock of the read; the catalog does not list it.
export const sunriseBakery: MockRestaurant = {
  id: "restaurant-sunrise-bakery",
  name: "Пекарня «Рассвет»",
  acceptingOrders: false,
  waitTime: usualWait,
  premiumFee: null,
  timeZone: berlinTime,
  openingHours: dailyHours("07:00", "11:00"),
  nextOpeningAt: null,
  scheduledSlots: [],
  menu: {
    items: [item("menu-cinnamon-roll", "Булочка с корицей", "190.00")],
  },
};

export const restaurantFixtures: readonly MockRestaurant[] = [
  kitchenEight,
  nightBakery,
  ...moreRestaurants,
  sunriseBakery,
];

// Restaurants that take scheduled orders (FR-672): one window holds this many.
export const scheduledRestaurantIds: ReadonlySet<string> = new Set([
  kitchenEight.id,
]);
export const slotCapacity = 2;

// Restaurants the mock user owns: openRestaurant and closeRestaurant succeed
// for them, any other existing restaurant answers PermissionDenied.
export const managedRestaurantIds: ReadonlySet<string> = new Set([
  kitchenEight.id,
  nightBakery.id,
  sunriseBakery.id,
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
