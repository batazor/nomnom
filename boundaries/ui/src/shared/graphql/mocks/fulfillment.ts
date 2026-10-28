import type {
  CourierAvailability,
  CourierDeliveryAction,
  KitchenAction,
  KitchenOrderStage,
  OrderMilestoneKind,
  OrderStatus,
} from "../generated/graphql";
import {
  type MockOrder,
  type MockRestaurant,
  restaurantSummary,
} from "./restaurant-fixtures";

// Kitchen and courier side of orders placed through the mock BFF, like
// restaurants and delivery would drive them: kitchen commands move the
// restaurant order, an accepted order gets a delivery that is offered to the
// courier while available, courier commands finish it. Every step updates the
// customer order the same way the order fulfillment workflow does.

type Clock = () => Date;

type KitchenOrder = {
  id: string;
  orderId: string;
  restaurantId: string;
  stage: KitchenOrderStage;
  receivedAt: string;
  // Moment the order joined the Kitchen Queue: set by ACCEPT.
  queuedAt?: string;
  // Cook assigned at the start of preparation (FR-225).
  cookId?: string;
  // Request of startNextPreparation that started the order.
  startRequestId?: string;
};

type MockCook = {
  id: string;
  name: string;
  onLine: boolean;
  retired: boolean;
  // Order of the last assignment: the free cook assigned longest ago goes first.
  lastAssigned: number;
};

type MockPoolCourier = {
  id: string;
  name: string;
  availability: CourierAvailability;
  retired: boolean;
};

type Delivery = {
  id: string;
  orderId: string;
  offerId: string;
  stage: "SEARCHING" | "ASSIGNED" | "PICKED_UP" | "COMPLETED";
};

type KitchenActionInput = {
  restaurantId: string;
  kitchenOrderId: string;
  action: KitchenAction;
};

const offerLifetimeMs = 5 * 60_000;

// Stage a kitchen command needs and the stage it leads to.
const kitchenTransitions: Record<
  Exclude<KitchenAction, "REJECT">,
  readonly [KitchenOrderStage, KitchenOrderStage]
> = {
  ACCEPT: ["RECEIVED", "QUEUED"],
  MARK_ASSEMBLED: ["PREPARING", "ASSEMBLED"],
  MARK_READY: ["ASSEMBLED", "READY"],
};

// Repeating it succeeds without a change, like restaurants does.
const repeatable = new Set<KitchenAction>(["MARK_READY"]);

const stageOrder: KitchenOrderStage[] = [
  "RECEIVED",
  "QUEUED",
  "PREPARING",
  "ASSEMBLED",
  "READY",
];

// Deadlines the mock kitchen and courier name, like the emulators of
// restaurants and delivery (FR-609, FR-610).
const preparationMs = 15 * 60_000;
const pickupMs = 10 * 60_000;
const deliveryMs = 15 * 60_000;

// Courier.id of the mock user, the entity key of courierChanged (ADR 36).
export const mockCourierId = "courier-demo";

// Cook Pool every mock restaurant starts with: enough cooks on line for the
// fixed scenarios in preparation, and Марат off line.
const initialCooks = [
  { name: "Айгуль", onLine: true },
  { name: "Олег", onLine: true },
  { name: "Дильноза", onLine: true },
  { name: "Марат", onLine: false },
  { name: "Лейла", onLine: true },
  { name: "Сергей", onLine: true },
] as const;

// Like CookName and CourierName: 1..64 characters after trim.
function poolName(name: string): string | null {
  const trimmed = name.trim();
  return trimmed.length === 0 || trimmed.length > 64 ? null : trimmed;
}

// Cooking and delivering stages that hold the cook.
const holdingStages = new Set<KitchenOrderStage>(["PREPARING", "ASSEMBLED"]);

export function createFulfillment({
  clock,
  orders,
  restaurant,
}: {
  clock: Clock;
  orders: Map<string, MockOrder>;
  restaurant: (id: string) => MockRestaurant | undefined;
}) {
  const kitchenOrders = new Map<string, KitchenOrder>();
  const deliveries = new Map<string, Delivery>();
  let availability: CourierAvailability = "UNAVAILABLE";
  const cookPools = new Map<string, MockCook[]>();
  let assignments = 0;
  let hired = 0;
  // The mock user is the courier "Илья"; the others act only in the pool.
  const couriers: MockPoolCourier[] = [
    {
      id: "courier-timur",
      name: "Тимур",
      availability: "AVAILABLE",
      retired: false,
    },
    {
      id: "courier-anna",
      name: "Анна",
      availability: "UNAVAILABLE",
      retired: false,
    },
  ];
  let demoRetired = false;
  let added = 0;

  function cooksOf(restaurantId: string): MockCook[] {
    let pool = cookPools.get(restaurantId);
    if (!pool) {
      pool = initialCooks.map((cook, index) => ({
        id: `cook-${restaurantId}-${index + 1}`,
        name: cook.name,
        onLine: cook.onLine,
        retired: false,
        lastAssigned: 0,
      }));
      cookPools.set(restaurantId, pool);
    }
    return pool;
  }

  function heldBy(cookId: string): KitchenOrder | undefined {
    return [...kitchenOrders.values()].find(
      (order) => order.cookId === cookId && holdingStages.has(order.stage),
    );
  }

  // Like Start Preparation: the free cook on line assigned longest ago.
  function assignCook(order: KitchenOrder): boolean {
    const cook = cooksOf(order.restaurantId)
      .filter((candidate) => candidate.onLine && !candidate.retired)
      .filter((candidate) => heldBy(candidate.id) === undefined)
      .sort(
        (a, b) => a.lastAssigned - b.lastAssigned || a.id.localeCompare(b.id),
      )[0];
    if (!cook) return false;
    assignments += 1;
    cook.lastAssigned = assignments;
    order.cookId = cook.id;
    return true;
  }

  function cookView(cook: MockCook) {
    return {
      id: cook.id,
      name: cook.name,
      onLine: cook.onLine,
      currentKitchenOrderId: heldBy(cook.id)?.id ?? null,
    };
  }

  function cookPool(restaurantId: string) {
    const cooks = cooksOf(restaurantId)
      .filter((cook) => !cook.retired)
      .sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id))
      .map(cookView);
    return {
      size: cooks.length,
      onLine: cooks.filter((cook) => cook.onLine).length,
      busy: cooks.filter((cook) => cook.currentKitchenOrderId !== null).length,
      queued: [...kitchenOrders.values()].filter(
        (order) =>
          order.restaurantId === restaurantId && order.stage === "QUEUED",
      ).length,
      cooks,
    };
  }

  function cookOf(restaurantId: string, cookId: string): MockCook {
    const cook = cooksOf(restaurantId).find(
      (candidate) => candidate.id === cookId,
    );
    if (!cook) throw new Error("Cook not found");
    return cook;
  }

  function hireCook(restaurantId: string, name: string) {
    const valid = poolName(name);
    if (valid === null) {
      return { cookPool: cookPool(restaurantId), problem: "COOK_NAME_INVALID" };
    }
    hired += 1;
    cooksOf(restaurantId).push({
      id: `cook-hired-${hired}`,
      name: valid,
      onLine: false,
      retired: false,
      lastAssigned: 0,
    });
    return { cookPool: cookPool(restaurantId), problem: null };
  }

  function setCookOnLine(
    restaurantId: string,
    cookId: string,
    onLine: boolean,
  ) {
    const cook = cookOf(restaurantId, cookId);
    if (onLine && cook.retired) {
      return { cookPool: cookPool(restaurantId), problem: "COOK_RETIRED" };
    }
    if (!onLine && heldBy(cook.id)) {
      return { cookPool: cookPool(restaurantId), problem: "COOK_BUSY" };
    }
    cook.onLine = onLine;
    return { cookPool: cookPool(restaurantId), problem: null };
  }

  function retireCook(restaurantId: string, cookId: string) {
    const cook = cookOf(restaurantId, cookId);
    if (cook.onLine) {
      return { cookPool: cookPool(restaurantId), problem: "COOK_ON_LINE" };
    }
    cook.retired = true;
    return { cookPool: cookPool(restaurantId), problem: null };
  }

  function after(ms: number): string {
    return new Date(clock().getTime() + ms).toISOString();
  }

  function advanceOrder(
    orderId: string,
    status: OrderStatus,
    milestone: OrderMilestoneKind,
    change: Partial<MockOrder> = {},
  ) {
    const order = orders.get(orderId);
    if (!order) return;
    orders.set(orderId, {
      ...order,
      ...change,
      status,
      cancellation: null,
      milestones: [
        ...order.milestones,
        { kind: milestone, occurredAt: clock().toISOString() },
      ],
    });
  }

  function received(order: MockOrder, stage: KitchenOrderStage = "RECEIVED") {
    const id = `kitchen-${order.id}`;
    const kitchenOrder = {
      id,
      orderId: order.id,
      restaurantId: order.restaurant.id,
      stage,
      receivedAt: order.placedAt,
    };
    kitchenOrders.set(id, kitchenOrder);
    if (holdingStages.has(stage)) assignCook(kitchenOrder);
    if (stage !== "RECEIVED") createDelivery(kitchenOrder);
  }

  // The payment is authorized before the restaurant gets the order: only a
  // paid order reaches the kitchen (FR-636).
  function paid(order: MockOrder) {
    // Unlike advanceOrder, the payment keeps the cancellation window.
    orders.set(order.id, {
      ...order,
      status: "PAID",
      milestones: [
        ...order.milestones,
        { kind: "PAID", occurredAt: clock().toISOString() },
      ],
    });
    received(order);
  }

  // Fixed scenarios still in the kitchen start there, so the board has work
  // before the first order is placed; an unpaid order is not there yet.
  for (const order of orders.values()) {
    if (order.status === "PAID") received(order);
    if (order.status === "PREPARING") received(order, "PREPARING");
  }

  // Acceptance confirms the customer order and names its readiness, so the
  // kitchen reads both from it, delays included (FR-609, FR-611); before
  // Acceptance there is neither.
  function kitchenView(order: KitchenOrder) {
    const placed = orders.get(order.orderId);
    if (!placed) throw new Error(`Order ${order.orderId} not found`);
    const accepted = order.stage !== "RECEIVED";
    const cook = order.cookId
      ? cooksOf(order.restaurantId).find(
          (candidate) => candidate.id === order.cookId && !candidate.retired,
        )
      : undefined;
    const confirmations = placed.milestones.filter(
      (milestone) => milestone.kind === "CONFIRMED",
    );
    return {
      ...order,
      items: placed.items.map((item) => ({
        name: item.menuItem.name,
        quantity: item.quantity,
      })),
      acceptedAt: accepted ? (confirmations.at(-1)?.occurredAt ?? null) : null,
      expectedReadyAt: accepted ? placed.expectedReadyAt : null,
      cook: cook ? cookView(cook) : null,
      priority: placed.priority,
    };
  }

  function kitchen(restaurantId: string) {
    const found = restaurant(restaurantId);
    if (!found) return null;
    // QUEUED orders in the order of the Kitchen Queue, like the BFF.
    const queue = queueOf(restaurantId);
    const active = [...kitchenOrders.values()]
      .filter((order) => order.restaurantId === restaurantId)
      .sort(
        (a, b) =>
          stageOrder.indexOf(a.stage) - stageOrder.indexOf(b.stage) ||
          queue.indexOf(a) - queue.indexOf(b) ||
          a.receivedAt.localeCompare(b.receivedAt),
      );
    return {
      restaurantId,
      restaurant: restaurantSummary(found),
      orders: active.map(kitchenView),
      cookPool: cookPool(restaurantId),
    };
  }

  function createDelivery(order: KitchenOrder) {
    const id = `delivery-${order.orderId}`;
    deliveries.set(order.orderId, {
      id,
      orderId: order.orderId,
      offerId: `offer-${order.orderId}`,
      stage: "SEARCHING",
    });
  }

  function applyKitchenAction({ kitchenOrderId, action }: KitchenActionInput) {
    const order = kitchenOrders.get(kitchenOrderId);
    if (!order) throw new Error("Restaurant order not found");
    if (action === "REJECT") {
      if (order.stage !== "RECEIVED") {
        return { order: kitchenView(order), problem: "ORDER_STAGE_CONFLICT" };
      }
      kitchenOrders.delete(kitchenOrderId);
      advanceOrder(order.orderId, "FAILED", "ENDED", {
        endReason: "RESTAURANT_REJECTED",
      });
      return { order: null, problem: null };
    }
    const [from, to] = kitchenTransitions[action];
    if (order.stage === to && repeatable.has(action)) {
      return { order: kitchenView(order), problem: null };
    }
    if (order.stage !== from) {
      return { order: kitchenView(order), problem: "ORDER_STAGE_CONFLICT" };
    }
    order.stage = to;
    if (action === "ACCEPT") {
      order.queuedAt = clock().toISOString();
      advanceOrder(order.orderId, "CONFIRMED", "CONFIRMED", {
        expectedReadyAt: after(preparationMs),
      });
      createDelivery(order);
    }
    if (action === "MARK_READY") {
      advanceOrder(order.orderId, "READY_FOR_PICKUP", "READY_FOR_PICKUP");
    }
    return { order: kitchenView(order), problem: null };
  }

  // Kitchen Queue of a restaurant like restaurants orders it (FR-241):
  // premium orders first, then by the moment they were queued.
  function queueOf(restaurantId: string): KitchenOrder[] {
    const premium = (order: KitchenOrder) =>
      orders.get(order.orderId)?.priority === "PREMIUM" ? 0 : 1;
    return [...kitchenOrders.values()]
      .filter(
        (order) =>
          order.restaurantId === restaurantId && order.stage === "QUEUED",
      )
      .sort(
        (a, b) =>
          premium(a) - premium(b) ||
          (a.queuedAt ?? a.receivedAt).localeCompare(
            b.queuedAt ?? b.receivedAt,
          ),
      );
  }

  // Starts the head of the queue (FR-225): a repeated request answers the
  // order it started, an empty queue starts nothing.
  function startNextPreparation(restaurantId: string, requestId: string) {
    const repeated = [...kitchenOrders.values()].find(
      (order) =>
        order.restaurantId === restaurantId &&
        order.startRequestId === requestId,
    );
    if (repeated) return { order: kitchenView(repeated), problem: null };
    const [head] = queueOf(restaurantId);
    if (!head) return { order: null, problem: null };
    if (!assignCook(head)) return { order: null, problem: "NO_COOK_AVAILABLE" };
    head.stage = "PREPARING";
    head.startRequestId = requestId;
    advanceOrder(head.orderId, "PREPARING", "PREPARATION_STARTED");
    return { order: kitchenView(head), problem: null };
  }

  function currentDelivery() {
    return [...deliveries.values()].find(
      (delivery) =>
        delivery.stage === "ASSIGNED" || delivery.stage === "PICKED_UP",
    );
  }

  function courier() {
    const current = currentDelivery();
    const expiresAt = new Date(
      clock().getTime() + offerLifetimeMs,
    ).toISOString();
    return {
      id: mockCourierId,
      availability,
      offers:
        availability === "AVAILABLE"
          ? [...deliveries.values()]
              .filter((delivery) => delivery.stage === "SEARCHING")
              .map((delivery) => ({
                id: delivery.offerId,
                deliveryId: delivery.id,
                orderId: delivery.orderId,
                expiresAt,
              }))
          : [],
      currentDelivery: current
        ? {
            id: current.id,
            orderId: current.orderId,
            stage: current.stage,
          }
        : null,
    };
  }

  function setAvailability(next: CourierAvailability) {
    if (next === "BUSY") throw new Error("BUSY is set by an assignment only");
    if (availability !== "BUSY") availability = next;
    return { courier: courier(), problem: null };
  }

  function acceptOffer(offerId: string) {
    const delivery = [...deliveries.values()].find(
      (candidate) => candidate.offerId === offerId,
    );
    if (delivery?.stage !== "SEARCHING" || availability !== "AVAILABLE") {
      return { courier: courier(), problem: "OFFER_NOT_AVAILABLE" };
    }
    delivery.stage = "ASSIGNED";
    availability = "BUSY";
    const order = orders.get(delivery.orderId);
    if (order) {
      orders.set(delivery.orderId, {
        ...order,
        expectedPickupAt: after(pickupMs),
        // The mock user is the courier «Илья» of the Courier Pool.
        courier: { name: "Илья" },
        milestones: [
          ...order.milestones,
          { kind: "COURIER_ASSIGNED", occurredAt: clock().toISOString() },
        ],
      });
    }
    return { courier: courier(), problem: null };
  }

  function applyDeliveryAction(
    deliveryId: string,
    action: CourierDeliveryAction,
  ) {
    const delivery = [...deliveries.values()].find(
      (candidate) => candidate.id === deliveryId,
    );
    if (!delivery) throw new Error("Delivery not found");
    const kitchenOrder = kitchenOrders.get(`kitchen-${delivery.orderId}`);
    if (action === "CONFIRM_PICKUP") {
      if (delivery.stage !== "ASSIGNED" || kitchenOrder?.stage !== "READY") {
        return { courier: courier(), problem: "DELIVERY_STAGE_CONFLICT" };
      }
      delivery.stage = "PICKED_UP";
      kitchenOrders.delete(kitchenOrder.id);
      advanceOrder(delivery.orderId, "PICKED_UP", "PICKED_UP", {
        expectedDeliveredAt: after(deliveryMs),
      });
      return { courier: courier(), problem: null };
    }
    if (delivery.stage !== "PICKED_UP") {
      return { courier: courier(), problem: "DELIVERY_STAGE_CONFLICT" };
    }
    delivery.stage = "COMPLETED";
    availability = "AVAILABLE";
    advanceOrder(delivery.orderId, "DELIVERED", "DELIVERED");
    return { courier: courier(), problem: null };
  }

  function poolCourierView(courier: MockPoolCourier) {
    return {
      id: courier.id,
      name: courier.name,
      availability: courier.availability,
      offeredOrderId: null,
      currentOrderId: null,
    };
  }

  // Like GetCourierPool: the mock user and the other couriers by name.
  function courierPool() {
    const own = courier();
    const searching = [...deliveries.values()].filter(
      (delivery) => delivery.stage === "SEARCHING",
    );
    const members = [
      ...(demoRetired
        ? []
        : [
            {
              id: mockCourierId,
              name: "Илья",
              availability,
              offeredOrderId: own.offers[0]?.orderId ?? null,
              currentOrderId: own.currentDelivery?.orderId ?? null,
            },
          ]),
      ...couriers.filter((courier) => !courier.retired).map(poolCourierView),
    ].sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
    return {
      // The pool id is its Market (ADR 41).
      id: "berlin",
      size: members.length,
      onLine: members.filter((member) => member.availability !== "UNAVAILABLE")
        .length,
      busy: members.filter((member) => member.availability === "BUSY").length,
      waiting: searching.length,
      couriers: members,
    };
  }

  function addCourier(name: string) {
    const valid = poolName(name);
    if (valid === null) {
      return { courierPool: courierPool(), problem: "COURIER_NAME_INVALID" };
    }
    added += 1;
    couriers.push({
      id: `courier-added-${added}`,
      name: valid,
      availability: "AVAILABLE",
      retired: false,
    });
    return { courierPool: courierPool(), problem: null };
  }

  function setCourierOnLine(courierId: string, onLine: boolean) {
    const next: CourierAvailability = onLine ? "AVAILABLE" : "UNAVAILABLE";
    if (courierId === mockCourierId) {
      if (availability === "BUSY" || (onLine && demoRetired)) {
        return {
          courierPool: courierPool(),
          problem: "COURIER_STATUS_CONFLICT",
        };
      }
      availability = next;
      return { courierPool: courierPool(), problem: null };
    }
    const courier = couriers.find((candidate) => candidate.id === courierId);
    if (!courier) throw new Error("Courier not found");
    if (courier.availability === "BUSY" || (onLine && courier.retired)) {
      return { courierPool: courierPool(), problem: "COURIER_STATUS_CONFLICT" };
    }
    courier.availability = next;
    return { courierPool: courierPool(), problem: null };
  }

  function retireCourier(courierId: string) {
    if (courierId === mockCourierId) {
      if (availability !== "UNAVAILABLE") {
        return { courierPool: courierPool(), problem: "COURIER_ON_LINE" };
      }
      demoRetired = true;
      return { courierPool: courierPool(), problem: null };
    }
    const courier = couriers.find((candidate) => candidate.id === courierId);
    if (!courier) throw new Error("Courier not found");
    if (courier.availability !== "UNAVAILABLE") {
      return { courierPool: courierPool(), problem: "COURIER_ON_LINE" };
    }
    courier.retired = true;
    return { courierPool: courierPool(), problem: null };
  }

  return {
    paid,
    kitchen,
    hireCook,
    setCookOnLine,
    retireCook,
    courierPool,
    addCourier,
    setCourierOnLine,
    retireCourier,
    applyKitchenAction,
    startNextPreparation,
    courier,
    setAvailability,
    acceptOffer,
    applyDeliveryAction,
  };
}
