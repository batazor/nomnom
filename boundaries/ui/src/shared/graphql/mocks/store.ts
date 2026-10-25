import { GraphQLError } from "graphql";

import {
  bffInfoFixture,
  createOrderScenarios,
  liveOrderAt,
  liveOrderId,
  liveOrderStepCount,
  type MockOrder,
  type MockRestaurant,
  managedRestaurantIds,
  orderLine,
  orderTotal,
  restaurantFixtures,
} from "./fixtures";
import { createFulfillment, mockCourierId } from "./fulfillment";
import { createChangeFeed } from "./order-changes";

type Clock = () => Date;

// order-live moves one status per step while someone follows it.
export const liveStepMs = 4_000;

type Connection<T> = {
  edges: { cursor: string; node: T }[];
  pageInfo: { hasNextPage: boolean; endCursor: string | null };
};

type PageArgs = { first: number; after?: string | null };

type PlaceOrderInput = {
  restaurantId: string;
  items: { menuItemId: string; quantity: number }[];
  idempotencyKey: string;
};

type PlaceOrderPayload = {
  order: MockOrder | null;
  problems: { code: string; menuItemIds: string[] }[];
};

type CancellationInput = {
  orderId: string;
  requestId: string;
  reason?: string | null;
};

type CancellationPayload = {
  outcome: "REQUESTED" | "ALREADY_CANCELLED" | null;
  problem: { code: string } | null;
};

type OpeningPayload = { restaurant: MockRestaurant; changed: boolean };

// The error shape Cosmo Router gives for a gRPC error of the BFF plugin: the
// code sits under extensions.errors[].extensions.code.
export function bffError(code: string, message: string): GraphQLError {
  return new GraphQLError("Failed to fetch from Subgraph 'bff'.", {
    extensions: { errors: [{ message, extensions: { code } }] },
  });
}

function paginate<T extends { id: string }>(
  items: readonly T[],
  { first, after }: PageArgs,
): Connection<T> {
  const start = after ? items.findIndex((item) => item.id === after) + 1 : 0;
  const page = items.slice(start, start + first);
  return {
    edges: page.map((node) => ({ cursor: node.id, node })),
    pageInfo: {
      hasNextPage: start + first < items.length,
      endCursor: page.at(-1)?.id ?? null,
    },
  };
}

const cancellationWindowMinutes = 5;

function initialState(now: Date) {
  const orders = new Map(
    createOrderScenarios(now).map((order) => [order.id, order]),
  );
  return {
    orders,
    liveStep: 0,
    liveStartedAt: now,
    liveCancelled: null as MockOrder | null,
    placed: new Map<string, PlaceOrderPayload>(),
    cancellations: new Map<string, CancellationPayload>(),
    // Opening state changed by openRestaurant/closeRestaurant, by restaurant id.
    opening: new Map<string, boolean>(),
    // The mock user is the operator; the Simulator starts off like a new service.
    simulatorEnabled: false,
  };
}

// In-memory backend behind the MSW handlers. Each instance keeps its own
// orders, order-live progress and idempotency keys. Every mutation notifies
// orderChanged subscribers, as Order Change Notifications of orders do.
export function createMockStore({
  now = () => new Date(),
}: {
  now?: Clock;
} = {}) {
  let clock = now;
  let state = initialState(clock());
  let fulfillment = newFulfillment();
  const changes = createChangeFeed();
  let liveTimer: ReturnType<typeof setInterval> | undefined;
  let liveFollowers = 0;

  // order-live progresses like a real kitchen and courier, without requests.
  function followLive(): () => void {
    liveFollowers += 1;
    liveTimer ??= setInterval(() => {
      if (state.liveCancelled || state.liveStep >= liveOrderStepCount - 1) {
        stopLive();
        return;
      }
      state.liveStep += 1;
      changes.changed();
    }, liveStepMs);
    return () => {
      liveFollowers -= 1;
      if (liveFollowers === 0) stopLive();
    };
  }

  function stopLive() {
    clearInterval(liveTimer);
    liveTimer = undefined;
  }

  // Runs a mutation and notifies subscribers of the orders it may change.
  function mutation<A, R>(resolve: (args: A) => R): (args: A) => R {
    return (args) => {
      const result = resolve(args);
      changes.changed();
      return result;
    };
  }

  function newFulfillment() {
    return createFulfillment({
      clock: () => clock(),
      orders: state.orders,
      restaurant: (id) => currentRestaurant(id),
    });
  }

  function liveOrder(): MockOrder {
    return (
      state.liveCancelled ?? liveOrderAt(state.liveStep, state.liveStartedAt)
    );
  }

  function findOrder(id: string): MockOrder | undefined {
    return id === liveOrderId ? liveOrder() : state.orders.get(id);
  }

  function currentRestaurant(id: string): MockRestaurant | undefined {
    const restaurant = restaurantFixtures.find((r) => r.id === id);
    if (!restaurant) return undefined;
    const open = state.opening.get(id);
    return open === undefined
      ? restaurant
      : { ...restaurant, acceptingOrders: open };
  }

  function currentRestaurants(): MockRestaurant[] {
    return restaurantFixtures.flatMap((r) => currentRestaurant(r.id) ?? []);
  }

  // Like restaurants OpenRestaurant/CloseRestaurant through the BFF: only the
  // owner may change the opening, a repeat answers changed = false.
  function setOpening(restaurantId: string, open: boolean): OpeningPayload {
    const restaurant = currentRestaurant(restaurantId);
    if (!restaurant) throw bffError("NotFound", "restaurant not found");
    if (!managedRestaurantIds.has(restaurantId)) {
      throw bffError(
        "PermissionDenied",
        "restaurant management is not permitted",
      );
    }
    const changed = restaurant.acceptingOrders !== open;
    state.opening.set(restaurantId, open);
    return {
      restaurant: { ...restaurant, acceptingOrders: open, menu: null },
      changed,
    };
  }

  function cancelled(order: MockOrder): MockOrder {
    return {
      ...order,
      status: "CANCELLED",
      endReason: "CUSTOMER_CANCELLED",
      milestones: [
        ...order.milestones,
        { kind: "ENDED", occurredAt: clock().toISOString() },
      ],
      cancellation: null,
    };
  }

  function decideCancellation(order: MockOrder): CancellationPayload {
    if (order.status === "CANCELLED")
      return { outcome: "ALREADY_CANCELLED", problem: null };
    if (order.status === "DELIVERED" || order.status === "FAILED") {
      return { outcome: null, problem: { code: "ORDER_ALREADY_FINAL" } };
    }
    const until = order.cancellation?.availableUntil;
    if (!until || clock().getTime() > Date.parse(until)) {
      return {
        outcome: null,
        problem: { code: "CANCELLATION_WINDOW_EXPIRED" },
      };
    }
    if (order.id === liveOrderId) state.liveCancelled = cancelled(order);
    else state.orders.set(order.id, cancelled(order));
    return { outcome: "REQUESTED", problem: null };
  }

  function placeOrder(input: PlaceOrderInput): PlaceOrderPayload {
    const restaurant = currentRestaurant(input.restaurantId);
    if (!restaurant) throw new Error("Restaurant not found");
    if (input.items.some((line) => line.quantity < 1)) {
      throw new Error("Quantity must be at least 1");
    }
    if (!restaurant.acceptingOrders) {
      return {
        order: null,
        problems: [
          { code: "RESTAURANT_NOT_ACCEPTING_ORDERS", menuItemIds: [] },
        ],
      };
    }
    const menu = restaurant.menu?.items ?? [];
    const unavailable = input.items
      .filter(
        (line) => !menu.find((item) => item.id === line.menuItemId)?.available,
      )
      .map((line) => line.menuItemId);
    if (unavailable.length > 0) {
      return {
        order: null,
        problems: [
          { code: "ORDER_ITEM_UNAVAILABLE", menuItemIds: unavailable },
        ],
      };
    }

    const placedAt = clock();
    const items = input.items.map((line) => {
      const item = menu.find((candidate) => candidate.id === line.menuItemId);
      if (!item) throw new Error("Menu item not found");
      return orderLine(item, line.quantity);
    });
    const order: MockOrder = {
      id: `order-placed-${state.orders.size + 1}`,
      status: "RECEIVED",
      endReason: null,
      restaurant,
      items,
      total: orderTotal(items),
      placedAt: placedAt.toISOString(),
      milestones: [{ kind: "PLACED", occurredAt: placedAt.toISOString() }],
      cancellation: {
        availableUntil: new Date(
          placedAt.getTime() + cancellationWindowMinutes * 60_000,
        ).toISOString(),
      },
    };
    state.orders.set(order.id, order);
    fulfillment.received(order);
    return { order, problems: [] };
  }

  const rootValue = {
    bffInfo: () => bffInfoFixture,
    // Like ListRestaurants by default, the catalog lists open restaurants only.
    restaurants: (args: PageArgs) =>
      paginate(
        currentRestaurants().filter((restaurant) => restaurant.acceptingOrders),
        args,
      ),
    restaurant: ({ id }: { id: string }) => currentRestaurant(id) ?? null,
    order: ({ id }: { id: string }) => findOrder(id) ?? null,
    // Like Cosmo Streams: the order after each change, an error for a missing one.
    orderChanged: ({ id }: { id: string }) =>
      changes.subscribe(
        () => {
          const order = findOrder(id);
          if (!order) throw bffError("NotFound", "order not found");
          return { orderChanged: order };
        },
        id === liveOrderId ? followLive() : undefined,
      ),
    // Like the Router (ADR 36): the board after each change, an error for a
    // kitchen the user may not see or another courier's id.
    kitchenChanged: ({ restaurantId }: { restaurantId: string }) =>
      changes.subscribe(() => {
        const kitchen = fulfillment.kitchen(restaurantId);
        if (!kitchen) throw bffError("NotFound", "kitchen not found");
        return { kitchenChanged: kitchen };
      }),
    courierChanged: ({ id }: { id: string }) => {
      if (id !== mockCourierId) throw bffError("NotFound", "courier not found");
      return changes.subscribe(() => ({
        courierChanged: fulfillment.courier(),
      }));
    },
    // Like ListOwnedRestaurants: open and closed restaurants of the mock user.
    myRestaurants: (args: PageArgs) =>
      paginate(
        currentRestaurants()
          .filter((restaurant) => managedRestaurantIds.has(restaurant.id))
          .map((restaurant) => ({ ...restaurant, menu: null })),
        args,
      ),
    customerOrders: (args: PageArgs) =>
      paginate(
        [...state.orders.values(), liveOrder()].sort((a, b) =>
          b.placedAt.localeCompare(a.placedAt),
        ),
        args,
      ),
    placeOrder: mutation(({ input }: { input: PlaceOrderInput }) => {
      const replay = state.placed.get(input.idempotencyKey);
      if (replay) return replay;
      const payload = placeOrder(input);
      state.placed.set(input.idempotencyKey, payload);
      return payload;
    }),
    requestOrderCancellation: mutation(
      ({ input }: { input: CancellationInput }) => {
        const replay = state.cancellations.get(input.requestId);
        if (replay) return replay;
        const order = findOrder(input.orderId);
        if (!order) throw new Error("Order not found");
        const payload = decideCancellation(order);
        state.cancellations.set(input.requestId, payload);
        return payload;
      },
    ),
    openRestaurant: ({ restaurantId }: { restaurantId: string }) =>
      setOpening(restaurantId, true),
    closeRestaurant: ({ restaurantId }: { restaurantId: string }) =>
      setOpening(restaurantId, false),
    // Like BFF: the Simulator of the three contexts, switched together.
    simulator: () => ({ enabled: state.simulatorEnabled }),
    setSimulatorEnabled: ({ enabled }: { enabled: boolean }) => {
      state.simulatorEnabled = enabled;
      return { enabled: state.simulatorEnabled };
    },
    // The mock user operates the kitchen of every restaurant and is a courier.
    kitchen: ({ restaurantId }: { restaurantId: string }) =>
      fulfillment.kitchen(restaurantId),
    applyKitchenAction: mutation(
      ({
        input,
      }: {
        input: Parameters<typeof fulfillment.applyKitchenAction>[0];
      }) => fulfillment.applyKitchenAction(input),
    ),
    courier: () => fulfillment.courier(),
    setCourierAvailability: mutation(
      ({
        availability,
      }: {
        availability: Parameters<typeof fulfillment.setAvailability>[0];
      }) => fulfillment.setAvailability(availability),
    ),
    acceptDeliveryOffer: mutation(({ offerId }: { offerId: string }) =>
      fulfillment.acceptOffer(offerId),
    ),
    applyCourierDeliveryAction: mutation(
      ({
        deliveryId,
        action,
      }: {
        deliveryId: string;
        action: Parameters<typeof fulfillment.applyDeliveryAction>[1];
      }) => fulfillment.applyDeliveryAction(deliveryId, action),
    ),
  };

  return {
    rootValue,
    setClock(next: Clock) {
      clock = next;
    },
    reset() {
      stopLive();
      clock = now;
      state = initialState(clock());
      fulfillment = newFulfillment();
    },
  };
}

export type MockStore = ReturnType<typeof createMockStore>;
