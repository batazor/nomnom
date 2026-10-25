import { execute, parse, subscribe } from "graphql";
import { describe, expect, it, vi } from "vitest";

import { kitchenEight, nightBakery, restaurantFixtures } from "./fixtures";
import { publicSchema } from "./schema";
import { createMockStore, liveStepMs, type MockStore } from "./store";

type OrderData = {
  order: { id: string; status: string; endReason: string | null } | null;
};
type PlaceOrderData = {
  placeOrder: {
    order: { id: string; status: string } | null;
    problems: { code: string; menuItemIds: string[] }[];
  };
};
type CancelData = {
  requestOrderCancellation: {
    outcome: string | null;
    problem: { code: string } | null;
  };
};

async function run<T>(
  store: MockStore,
  source: string,
  variables: Record<string, unknown> = {},
): Promise<T> {
  const result = await execute({
    schema: publicSchema,
    document: parse(source),
    rootValue: store.rootValue,
    variableValues: variables,
  });
  expect(result.errors).toBeUndefined();
  return result.data as T;
}

type OpeningData = {
  openRestaurant: {
    changed: boolean;
    restaurant: { id: string; acceptingOrders: boolean };
  };
};

const openRestaurant = `mutation ($restaurantId: ID!) {
  openRestaurant(restaurantId: $restaurantId) { changed restaurant { id acceptingOrders } }
}`;
const closeRestaurant = `mutation ($restaurantId: ID!) {
  closeRestaurant(restaurantId: $restaurantId) { changed restaurant { id acceptingOrders } }
}`;

const orderStatus =
  "query ($id: ID!) { order(id: $id) { id status endReason } }";
const placeOrder = `mutation ($input: PlaceOrderInput!) {
  placeOrder(input: $input) { order { id status } problems { code menuItemIds } }
}`;
const cancelOrder = `mutation ($input: RequestOrderCancellationInput!) {
  requestOrderCancellation(input: $input) { outcome problem { code } }
}`;

function placeInput(overrides: Record<string, unknown> = {}) {
  return {
    input: {
      restaurantId: kitchenEight.id,
      items: [{ menuItemId: "menu-tom-yum", quantity: 2 }],
      deliveryAddress: "ул. Лесная, 12",
      idempotencyKey: "checkout-1",
      ...overrides,
    },
  };
}

describe("mock store", () => {
  it("lists open restaurants only, page by page", async () => {
    const store = createMockStore();
    const list = `query ($first: Int!, $after: String) {
      restaurants(first: $first, after: $after) {
        edges { node { id acceptingOrders } }
        pageInfo { hasNextPage endCursor }
      }
    }`;
    type ListData = {
      restaurants: {
        edges: { node: { id: string; acceptingOrders: boolean } }[];
        pageInfo: { hasNextPage: boolean; endCursor: string | null };
      };
    };
    const first = await run<ListData>(store, list, { first: 4 });
    const rest = await run<ListData>(store, list, {
      first: 4,
      after: first.restaurants.pageInfo.endCursor,
    });
    const nodes = [...first.restaurants.edges, ...rest.restaurants.edges].map(
      (edge) => edge.node,
    );

    expect(first.restaurants.pageInfo.hasNextPage).toBe(true);
    expect(rest.restaurants.pageInfo.hasNextPage).toBe(false);
    expect(nodes.every((node) => node.acceptingOrders)).toBe(true);
    expect(nodes.map((node) => node.id)).not.toContain(nightBakery.id);
  });

  it("returns null for a missing order", async () => {
    const data = await run<OrderData>(createMockStore(), orderStatus, {
      id: "order-unknown",
    });
    expect(data.order).toBeNull();
  });

  it("maps a restaurant rejection to FAILED with its end reason", async () => {
    const data = await run<OrderData>(createMockStore(), orderStatus, {
      id: "order-restaurant-rejected",
    });
    expect(data.order).toMatchObject({
      status: "FAILED",
      endReason: "RESTAURANT_REJECTED",
    });
  });

  it("pushes order-live one status per step up to DELIVERED, not per request", async () => {
    vi.useFakeTimers();
    const store = createMockStore();
    const stream = await subscribe({
      schema: publicSchema,
      document: parse(
        "subscription ($id: ID!) { orderChanged(id: $id) { status } }",
      ),
      rootValue: store.rootValue,
      variableValues: { id: "order-live" },
    });
    if (!(Symbol.asyncIterator in stream)) throw new Error("no stream");

    await run<OrderData>(store, orderStatus, { id: "order-live" });
    const statuses: string[] = [];
    for (let i = 0; i < 5; i += 1) {
      const next = stream.next();
      await vi.advanceTimersByTimeAsync(liveStepMs);
      const { value } = await next;
      const data = value?.data as { orderChanged: { status: string } };
      statuses.push(data.orderChanged.status);
    }
    await stream.return?.();
    vi.useRealTimers();

    expect(statuses).toEqual([
      "CONFIRMED",
      "PREPARING",
      "READY_FOR_PICKUP",
      "PICKED_UP",
      "DELIVERED",
    ]);
  });

  it("notifies orderChanged subscribers after a cancellation", async () => {
    const store = createMockStore();
    const stream = await subscribe({
      schema: publicSchema,
      document: parse(
        "subscription ($id: ID!) { orderChanged(id: $id) { status } }",
      ),
      rootValue: store.rootValue,
      variableValues: { id: "order-received" },
    });
    if (!(Symbol.asyncIterator in stream)) throw new Error("no stream");
    const next = stream.next();

    await run<CancelData>(store, cancelOrder, {
      input: { orderId: "order-received", requestId: "cancel-push" },
    });

    expect((await next).value?.data).toEqual({
      orderChanged: { status: "CANCELLED" },
    });
    await stream.return?.();
  });

  it("reports unavailable items and closed restaurants as problems", async () => {
    const store = createMockStore();
    const unavailable = await run<PlaceOrderData>(
      store,
      placeOrder,
      placeInput({ items: [{ menuItemId: "menu-gyoza", quantity: 1 }] }),
    );
    expect(unavailable.placeOrder).toEqual({
      order: null,
      problems: [
        { code: "ORDER_ITEM_UNAVAILABLE", menuItemIds: ["menu-gyoza"] },
      ],
    });

    const closed = await run<PlaceOrderData>(
      store,
      placeOrder,
      placeInput({
        restaurantId: nightBakery.id,
        items: [{ menuItemId: "menu-croissant", quantity: 1 }],
        idempotencyKey: "checkout-2",
      }),
    );
    expect(closed.placeOrder.problems).toEqual([
      { code: "RESTAURANT_NOT_ACCEPTING_ORDERS", menuItemIds: [] },
    ]);
  });

  it("replays placeOrder with the same idempotency key", async () => {
    const store = createMockStore();
    const first = await run<PlaceOrderData>(store, placeOrder, placeInput());
    const second = await run<PlaceOrderData>(store, placeOrder, placeInput());
    const other = await run<PlaceOrderData>(
      store,
      placeOrder,
      placeInput({ idempotencyKey: "checkout-3" }),
    );

    expect(first.placeOrder.order?.status).toBe("RECEIVED");
    expect(second.placeOrder.order?.id).toBe(first.placeOrder.order?.id);
    expect(other.placeOrder.order?.id).not.toBe(first.placeOrder.order?.id);
  });

  it("accepts cancellation inside the window and replays it by request id", async () => {
    const store = createMockStore();
    const input = {
      input: { orderId: "order-received", requestId: "cancel-1" },
    };

    expect(
      (await run<CancelData>(store, cancelOrder, input))
        .requestOrderCancellation,
    ).toEqual({
      outcome: "REQUESTED",
      problem: null,
    });
    expect(
      (await run<CancelData>(store, cancelOrder, input))
        .requestOrderCancellation.outcome,
    ).toBe("REQUESTED");
    expect(
      (await run<OrderData>(store, orderStatus, { id: "order-received" }))
        .order,
    ).toMatchObject({
      status: "CANCELLED",
      endReason: "CUSTOMER_CANCELLED",
    });
    const again = await run<CancelData>(store, cancelOrder, {
      input: { orderId: "order-received", requestId: "cancel-2" },
    });
    expect(again.requestOrderCancellation.outcome).toBe("ALREADY_CANCELLED");
  });

  it("rejects cancellation after the window closes", async () => {
    const store = createMockStore();
    store.setClock(() => new Date(Date.now() + 10 * 60_000));
    const data = await run<CancelData>(store, cancelOrder, {
      input: { orderId: "order-received", requestId: "cancel-late" },
    });
    expect(data.requestOrderCancellation).toEqual({
      outcome: null,
      problem: { code: "CANCELLATION_WINDOW_EXPIRED" },
    });
  });

  it("rejects cancellation of a delivered order", async () => {
    const data = await run<CancelData>(createMockStore(), cancelOrder, {
      input: { orderId: "order-delivered", requestId: "cancel-final" },
    });
    expect(data.requestOrderCancellation.problem).toEqual({
      code: "ORDER_ALREADY_FINAL",
    });
  });

  it("opens and closes an owned restaurant idempotently", async () => {
    const store = createMockStore();
    const open = await run<OpeningData>(store, openRestaurant, {
      restaurantId: nightBakery.id,
    });
    expect(open.openRestaurant).toEqual({
      changed: true,
      restaurant: { id: nightBakery.id, acceptingOrders: true },
    });
    const again = await run<OpeningData>(store, openRestaurant, {
      restaurantId: nightBakery.id,
    });
    expect(again.openRestaurant.changed).toBe(false);
    const catalog = await run<{ restaurants: { edges: unknown[] } }>(
      store,
      "{ restaurants(first: 100) { edges { cursor } } }",
    );
    expect(catalog.restaurants.edges).toHaveLength(restaurantFixtures.length);

    store.reset();
    expect(
      (
        await run<{ restaurant: { acceptingOrders: boolean } }>(
          store,
          "query ($id: ID!) { restaurant(id: $id) { acceptingOrders } }",
          { id: nightBakery.id },
        )
      ).restaurant.acceptingOrders,
    ).toBe(false);
  });

  it("refuses a closed restaurant's orders after closing it", async () => {
    const store = createMockStore();
    await run(store, closeRestaurant, { restaurantId: kitchenEight.id });
    const data = await run<PlaceOrderData>(store, placeOrder, placeInput());
    expect(data.placeOrder.problems).toEqual([
      { code: "RESTAURANT_NOT_ACCEPTING_ORDERS", menuItemIds: [] },
    ]);
  });

  it.each([
    ["restaurant-green-bowl", "PermissionDenied"],
    ["restaurant-missing", "NotFound"],
  ])("answers %s with the BFF error %s", async (restaurantId, code) => {
    const result = await execute({
      schema: publicSchema,
      document: parse(openRestaurant),
      rootValue: createMockStore().rootValue,
      variableValues: { restaurantId },
    });
    expect(result.data).toBeNull();
    expect(result.errors?.[0]?.extensions).toEqual({
      errors: [{ message: expect.any(String), extensions: { code } }],
    });
  });
});
