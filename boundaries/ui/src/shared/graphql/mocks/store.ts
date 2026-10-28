import { GraphQLError } from "graphql";
import { courierMoves } from "./courier-tracks";
import {
  bffInfoFixture,
  createOrderScenarios,
  delayedOrderId,
  liveOrderAt,
  liveOrderId,
  liveOrderStepCount,
  type MockDeliveryWindow,
  type MockOpeningInterval,
  type MockOrder,
  type MockRestaurant,
  type MockRestaurantSummary,
  managedRestaurantIds,
  orderLine,
  orderTotal,
  preparationDelayed,
  restaurantFixtures,
  restaurantSummary,
  scheduledRestaurantIds,
  slotCapacity,
  sunriseBakery,
  withFee,
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
  priority: "STANDARD" | "PREMIUM";
  deliveryWindowStart?: string | null;
};

type PlaceOrderProblem = {
  code: string;
  menuItemIds: string[];
  alternativeWindows: MockDeliveryWindow[];
};

type PlaceOrderPayload = {
  order: MockOrder | null;
  problems: PlaceOrderProblem[];
};

function problem(
  code: string,
  menuItemIds: string[] = [],
  alternativeWindows: MockDeliveryWindow[] = [],
): PlaceOrderProblem {
  return { code, menuItemIds, alternativeWindows };
}

// Windows of scheduled orders: SCHEDULED_SLOT__WIDTH on a UTC grid, from the
// minimum lead over the horizon, like restaurants lists them (FR-673).
const slotWidthMs = 15 * 60_000;
const slotLeadMs = 60 * 60_000;
const slotHorizonMs = 4 * 60 * 60_000;
// The third window of the horizon is already full, so the list shows one.
const fullSlotIndex = 2;

type CancellationInput = {
  orderId: string;
  requestId: string;
  reason?: string | null;
};

type CancellationPayload = {
  outcome: "REQUESTED" | "ALREADY_CANCELLED" | null;
  problem: { code: string } | null;
};

type OpeningPayload = { restaurant: MockRestaurantSummary; changed: boolean };

type OpeningHoursPayload = { restaurant: MockRestaurant; changed: boolean };

const hhmm = /^([01]\d|2[0-3]):([0-5]\d)$/;

// Like Set Opening Hours of restaurants (FR-697): the offending interval is
// named in the INVALID_ARGUMENT the BFF passes on.
function validateOpeningHours(hours: readonly MockOpeningInterval[]): void {
  const reject = (reason: string, interval: MockOpeningInterval) => {
    throw bffError(
      "InvalidArgument",
      `invalid opening hours: ${reason} at day ${interval.dayOfWeek} ${interval.opens}-${interval.closes}`,
    );
  };
  const day = 24 * 60;
  const week = 7 * day;
  const spans = hours.map((interval) => {
    if (
      !Number.isInteger(interval.dayOfWeek) ||
      interval.dayOfWeek < 1 ||
      interval.dayOfWeek > 7
    ) {
      reject("invalid_day", interval);
    }
    const opens = hhmm.exec(interval.opens);
    const closes = hhmm.exec(interval.closes);
    if (!opens || !closes) return reject("invalid_time", interval);
    const from = Number(opens[1]) * 60 + Number(opens[2]);
    const to = Number(closes[1]) * 60 + Number(closes[2]);
    if (from === to) reject("empty_interval", interval);
    const start = (interval.dayOfWeek - 1) * day + from;
    return {
      interval,
      start,
      end: start + (to > from ? to - from : day - from + to),
    };
  });
  spans.sort((a, b) => a.start - b.start);
  for (const [i, a] of spans.entries()) {
    for (const b of spans.slice(i + 1)) {
      if (
        [-week, 0, week].some(
          (shift) => a.start < b.end + shift && b.start + shift < a.end,
        )
      ) {
        reject("overlap", b.interval);
      }
    }
  }
}

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
    // order-delayed gets its later readiness once.
    preparationDelayed: false,
    placed: new Map<string, PlaceOrderPayload>(),
    cancellations: new Map<string, CancellationPayload>(),
    // Opening state changed by openRestaurant/closeRestaurant, by restaurant id.
    opening: new Map<string, boolean>(),
    // Opening hours replaced by setOpeningHours, by restaurant id.
    openingHours: new Map<string, MockOpeningInterval[]>(),
    // Reserved places of scheduled orders by "<restaurantId> <window start>".
    reservations: new Map<string, number>(),
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
  let delayTimer: ReturnType<typeof setTimeout> | undefined;

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

  // order-delayed: one step after someone follows it, the kitchen names a
  // later readiness, pushed like any other change.
  function followDelayed(): () => void {
    if (!state.preparationDelayed) {
      delayTimer ??= setTimeout(() => {
        delayTimer = undefined;
        const order = state.orders.get(delayedOrderId);
        if (!order || state.preparationDelayed) return;
        state.preparationDelayed = true;
        state.orders.set(delayedOrderId, preparationDelayed(order));
        changes.changed();
      }, liveStepMs);
    }
    return () => {};
  }

  function stopDelayed() {
    clearTimeout(delayTimer);
    delayTimer = undefined;
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
    const fixture = restaurantFixtures.find((r) => r.id === id);
    if (!fixture) return undefined;
    const open = state.opening.get(id);
    const restaurant: MockRestaurant = {
      ...fixture,
      acceptingOrders: open ?? fixture.acceptingOrders,
      openingHours: state.openingHours.get(id) ?? fixture.openingHours,
    };
    // Opened by its owner before its hours: it opens on the next full hour
    // after two hours of the clock; closed by the owner, it names nothing.
    if (id === sunriseBakery.id && open !== false) {
      const opensAt = new Date(clock().getTime() + 2 * 60 * 60_000);
      opensAt.setUTCMinutes(0, 0, 0);
      restaurant.acceptingOrders = false;
      restaurant.nextOpeningAt = opensAt.toISOString();
    }
    restaurant.scheduledSlots = scheduledSlots(restaurant);
    return restaurant;
  }

  // Empty for a restaurant without scheduled orders or not accepting now,
  // like ListScheduledSlots.
  function scheduledSlots(restaurant: MockRestaurant): MockDeliveryWindow[] {
    if (
      !scheduledRestaurantIds.has(restaurant.id) ||
      !restaurant.acceptingOrders
    ) {
      return [];
    }
    const first =
      Math.ceil((clock().getTime() + slotLeadMs) / slotWidthMs) * slotWidthMs;
    return Array.from({ length: slotHorizonMs / slotWidthMs }, (_, index) => {
      const start = new Date(first + index * slotWidthMs).toISOString();
      const reserved = state.reservations.get(`${restaurant.id} ${start}`) ?? 0;
      return {
        start,
        end: new Date(first + (index + 1) * slotWidthMs).toISOString(),
        available: index !== fullSlotIndex && reserved < slotCapacity,
      };
    });
  }

  // Like Set Opening Hours through the BFF: only the owner, the whole
  // schedule, the restaurant read again.
  function setOpeningHours(
    restaurantId: string,
    hours: MockOpeningInterval[],
  ): OpeningHoursPayload {
    const restaurant = currentRestaurant(restaurantId);
    if (!restaurant) throw bffError("NotFound", "restaurant not found");
    if (!managedRestaurantIds.has(restaurantId)) {
      throw bffError(
        "PermissionDenied",
        "restaurant management is not permitted",
      );
    }
    validateOpeningHours(hours);
    const changed =
      JSON.stringify(restaurant.openingHours) !== JSON.stringify(hours);
    state.openingHours.set(
      restaurantId,
      hours.map(({ dayOfWeek, opens, closes }) => ({
        dayOfWeek,
        opens,
        closes,
      })),
    );
    const saved = currentRestaurant(restaurantId);
    if (!saved) throw bffError("NotFound", "restaurant not found");
    return { restaurant: saved, changed };
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
      restaurant: { ...restaurantSummary(restaurant), acceptingOrders: open },
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
        problems: [problem("RESTAURANT_NOT_ACCEPTING_ORDERS")],
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
        problems: [problem("ORDER_ITEM_UNAVAILABLE", unavailable)],
      };
    }

    // PREMIUM needs the fee of the restaurant (FR-650), which is fixed in
    // the order and added to its total.
    const priority = input.priority;
    const premiumFee = priority === "PREMIUM" ? restaurant.premiumFee : null;
    if (priority === "PREMIUM" && !premiumFee) {
      return {
        order: null,
        problems: [problem("PREMIUM_UNAVAILABLE")],
      };
    }
    // A scheduled order reserves its window (FR-681); a full one offers the
    // nearest available windows (FR-692). PREMIUM goes with a window too.
    const windowStart = input.deliveryWindowStart ?? null;
    let deliveryWindow: MockOrder["deliveryWindow"] = null;
    if (windowStart !== null) {
      if (!scheduledRestaurantIds.has(restaurant.id)) {
        return {
          order: null,
          problems: [problem("SCHEDULED_ORDERS_UNAVAILABLE")],
        };
      }
      const slots = restaurant.scheduledSlots;
      const chosen = slots.find((slot) => slot.start === windowStart);
      if (!chosen) throw bffError("InvalidArgument", "window is off the grid");
      if (!chosen.available) {
        const later = slots.filter(
          (slot) => slot.available && slot.start > windowStart,
        );
        return {
          order: null,
          problems: [problem("SLOT_UNAVAILABLE", [], later.slice(0, 3))],
        };
      }
      const key = `${restaurant.id} ${windowStart}`;
      state.reservations.set(key, (state.reservations.get(key) ?? 0) + 1);
      deliveryWindow = { start: chosen.start, end: chosen.end };
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
      restaurant: restaurantSummary(restaurant),
      items,
      total: premiumFee
        ? withFee(orderTotal(items), premiumFee)
        : orderTotal(items),
      priority,
      premiumFee,
      placedAt: placedAt.toISOString(),
      milestones: [{ kind: "PLACED", occurredAt: placedAt.toISOString() }],
      cancellation: {
        availableUntil: new Date(
          placedAt.getTime() + cancellationWindowMinutes * 60_000,
        ).toISOString(),
      },
      expectedReadyAt: null,
      expectedPickupAt: null,
      expectedDeliveredAt: null,
      deliveryWindow,
    };
    state.orders.set(order.id, order);
    fulfillment.paid(order);
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
        id === liveOrderId
          ? followLive()
          : id === delayedOrderId
            ? followDelayed()
            : undefined,
      ),
    // Like the Router (ADR 36): the board after each change, an error for a
    // kitchen the user may not see or another courier's id.
    kitchenChanged: ({ restaurantId }: { restaurantId: string }) =>
      changes.subscribe(() => {
        const kitchen = fulfillment.kitchen(restaurantId);
        if (!kitchen) throw bffError("NotFound", "kitchen not found");
        return { kitchenChanged: kitchen };
      }),
    courierPoolChanged: ({ marketId }: { marketId: string }) =>
      changes.subscribe(() => {
        const pool = fulfillment.courierPool();
        if (pool.id !== marketId)
          throw bffError("NotFound", "courier pool not found");
        return { courierPoolChanged: pool };
      }),
    // Like the Router: the courier positions of a tracking channel; an
    // unknown channel never yields.
    courierMoved: ({ channel }: { channel: string }) => {
      const order = [liveOrder(), ...state.orders.values()].find(
        (candidate) => candidate.tracking?.channel === channel,
      );
      return courierMoves(
        channel,
        order?.tracking ? order.status === "PICKED_UP" : null,
        () => clock(),
      );
    },
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
          .map(restaurantSummary),
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
    setOpeningHours: ({
      restaurantId,
      openingHours,
    }: {
      restaurantId: string;
      openingHours: MockOpeningInterval[];
    }) => setOpeningHours(restaurantId, openingHours),
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
    startNextPreparation: mutation(
      ({
        restaurantId,
        requestId,
      }: {
        restaurantId: string;
        requestId: string;
      }) => fulfillment.startNextPreparation(restaurantId, requestId),
    ),
    hireCook: mutation(
      ({ restaurantId, name }: { restaurantId: string; name: string }) =>
        fulfillment.hireCook(restaurantId, name),
    ),
    setCookOnLine: mutation(
      ({
        restaurantId,
        cookId,
        onLine,
      }: {
        restaurantId: string;
        cookId: string;
        onLine: boolean;
      }) => fulfillment.setCookOnLine(restaurantId, cookId, onLine),
    ),
    retireCook: mutation(
      ({ restaurantId, cookId }: { restaurantId: string; cookId: string }) =>
        fulfillment.retireCook(restaurantId, cookId),
    ),
    // The mock user is a dispatcher of the Courier Pool too.
    courierPool: () => fulfillment.courierPool(),
    addCourier: mutation(({ name }: { name: string }) =>
      fulfillment.addCourier(name),
    ),
    setCourierOnLine: mutation(
      ({ courierId, onLine }: { courierId: string; onLine: boolean }) =>
        fulfillment.setCourierOnLine(courierId, onLine),
    ),
    retireCourier: mutation(({ courierId }: { courierId: string }) =>
      fulfillment.retireCourier(courierId),
    ),
    offerDeliveryToCourier: mutation(
      ({ orderId, courierId }: { orderId: string; courierId: string }) =>
        fulfillment.offerDeliveryToCourier(orderId, courierId),
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
      stopDelayed();
      clock = now;
      state = initialState(clock());
      fulfillment = newFulfillment();
    },
  };
}

export type MockStore = ReturnType<typeof createMockStore>;
