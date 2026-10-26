import type {
  CourierAvailability,
  CourierDeliveryAction,
  KitchenAction,
  KitchenOrderStage,
  OrderMilestoneKind,
  OrderStatus,
} from "../generated/graphql";
import type { MockOrder, MockRestaurant } from "./restaurant-fixtures";

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
  START_PREPARATION: ["QUEUED", "PREPARING"],
  MARK_ASSEMBLED: ["PREPARING", "ASSEMBLED"],
  MARK_READY: ["ASSEMBLED", "READY"],
};

// Repeating these succeeds without a change, like restaurants does.
const repeatable = new Set<KitchenAction>(["START_PREPARATION", "MARK_READY"]);

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
    if (stage !== "RECEIVED") createDelivery(kitchenOrder);
  }

  // Fixed scenarios still in the kitchen start there, so the board has work
  // before the first order is placed.
  for (const order of orders.values()) {
    if (order.status === "RECEIVED") received(order);
    if (order.status === "PREPARING") received(order, "PREPARING");
  }

  // Acceptance confirms the customer order and names its readiness, so the
  // kitchen reads both from it, delays included (FR-609, FR-611); before
  // Acceptance there is neither.
  function kitchenView(order: KitchenOrder) {
    const placed = orders.get(order.orderId);
    if (!placed) throw new Error(`Order ${order.orderId} not found`);
    const accepted = order.stage !== "RECEIVED";
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
    };
  }

  function kitchen(restaurantId: string) {
    const found = restaurant(restaurantId);
    if (!found) return null;
    const active = [...kitchenOrders.values()]
      .filter((order) => order.restaurantId === restaurantId)
      .sort(
        (a, b) =>
          stageOrder.indexOf(a.stage) - stageOrder.indexOf(b.stage) ||
          a.receivedAt.localeCompare(b.receivedAt),
      );
    return {
      restaurantId,
      restaurant: { ...found, menu: null },
      orders: active.map(kitchenView),
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
      advanceOrder(order.orderId, "CONFIRMED", "CONFIRMED", {
        expectedReadyAt: after(preparationMs),
      });
      createDelivery(order);
    }
    if (action === "START_PREPARATION") {
      advanceOrder(order.orderId, "PREPARING", "PREPARATION_STARTED");
    }
    if (action === "MARK_READY") {
      advanceOrder(order.orderId, "READY_FOR_PICKUP", "READY_FOR_PICKUP");
    }
    return { order: kitchenView(order), problem: null };
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

  return {
    received,
    kitchen,
    applyKitchenAction,
    courier,
    setAvailability,
    acceptOffer,
    applyDeliveryAction,
  };
}
