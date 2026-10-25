import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse } from "msw";
import { graphql } from "msw/graphql";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";

import { AppProviders } from "../../app/providers";
import { AppRouter } from "../../app/router";
import { mockStore } from "../../shared/graphql/mocks/handlers";
import { mockServer } from "../../shared/graphql/mocks/server";

const config = {
  publicGraphqlUrl: "https://public.example.test/graphql",
  locale: "ru" as const,
  enableMocks: true,
  environment: "test",
};

const kitchen = "restaurant-kitchen-8";
const cartKey = `nomnom.cart.v1.${kitchen}`;
const publicGraphql = graphql.link(/\/graphql$/);

beforeAll(() => mockServer.listen({ onUnhandledFrame: "error" }));
afterEach(() => {
  mockServer.resetHandlers();
  mockStore.reset();
  localStorage.clear();
});
afterAll(() => mockServer.close());

type PlaceOrderVariables = {
  input: {
    restaurantId: string;
    items: { menuItemId: string; quantity: number }[];
    idempotencyKey: string;
  };
};

function storeCart(lines: { menuItemId: string; quantity: number }[]) {
  localStorage.setItem(cartKey, JSON.stringify({ lines }));
}

// Records every placeOrder request and lets it through to the mock store.
function recordPlaceOrder() {
  const requests: PlaceOrderVariables["input"][] = [];
  mockServer.use(
    publicGraphql.mutation<never, PlaceOrderVariables>(
      "PlaceOrder",
      ({ variables }) => {
        requests.push(variables.input);
      },
    ),
  );
  return requests;
}

async function openCheckout() {
  const user = userEvent.setup();
  render(
    <AppProviders config={config}>
      <AppRouter initialEntries={["/"]} />
    </AppProviders>,
  );
  await user.click(await screen.findByRole("button", { name: /^Кухня №8/ }));
  await screen.findByRole("heading", { level: 3, name: "Кухня №8" });
  return user;
}

function cartLine(name: string) {
  const cart = screen.getByRole("region", { name: "Корзина" });
  const line = within(cart)
    .getAllByRole("listitem")
    .find((item) => item.textContent?.includes(name));
  if (!line) throw new Error(`No cart line ${name}`);
  return line;
}

describe("place order", () => {
  it("places the cart and shows the placed order on the board", async () => {
    storeCart([
      { menuItemId: "menu-tom-yum", quantity: 2 },
      { menuItemId: "menu-bao", quantity: 1 },
    ]);
    const requests = recordPlaceOrder();
    const user = await openCheckout();
    await user.click(screen.getByRole("button", { name: "Оформить заказ" }));

    expect(
      await screen.findByRole("button", { name: /Кухня №8.*Оформлен/ }),
    ).toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Подробнее о заказе" }),
    );
    expect(await screen.findByTestId("order-total")).toHaveTextContent(
      /1.?800/,
    );
    expect(requests).toEqual([
      {
        restaurantId: kitchen,
        items: [
          { menuItemId: "menu-tom-yum", quantity: 2 },
          { menuItemId: "menu-bao", quantity: 1 },
        ],
        idempotencyKey: expect.stringMatching(/^[0-9a-f-]{36}$/),
      },
    ]);
    expect(localStorage.getItem(cartKey)).toBeNull();
  });

  it("marks an item that ran out and keeps the cart", async () => {
    // The gyoza was added while it was still available.
    storeCart([
      { menuItemId: "menu-bao", quantity: 1 },
      { menuItemId: "menu-gyoza", quantity: 1 },
    ]);
    const user = await openCheckout();
    await user.click(screen.getByRole("button", { name: "Оформить заказ" }));

    expect(
      await within(cartLine("Гёдза с уткой")).findByText(
        "Закончилось, уберите из корзины",
      ),
    ).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Некоторые блюда нельзя заказать: они отмечены в корзине.",
    );
    expect(
      within(cartLine("Бао с грибами")).queryByText(/Закончилось/),
    ).not.toBeInTheDocument();
    expect(JSON.parse(localStorage.getItem(cartKey) ?? "null")).toEqual({
      lines: [
        { menuItemId: "menu-bao", quantity: 1 },
        { menuItemId: "menu-gyoza", quantity: 1 },
      ],
    });

    await user.click(
      screen.getByRole("button", { name: "Убрать из корзины: Гёдза с уткой" }),
    );
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Оформить заказ" }));
    expect(
      await screen.findByRole("button", { name: /Кухня №8.*Оформлен/ }),
    ).toBeInTheDocument();
  });

  it("shows a whole-order problem above the form", async () => {
    storeCart([{ menuItemId: "menu-bao", quantity: 1 }]);
    mockServer.use(
      publicGraphql.mutation("PlaceOrder", () =>
        HttpResponse.json({
          data: {
            placeOrder: {
              order: null,
              problems: [
                { code: "RESTAURANT_NOT_ACCEPTING_ORDERS", menuItemIds: [] },
              ],
            },
          },
        }),
      ),
    );
    const user = await openCheckout();
    await user.click(screen.getByRole("button", { name: "Оформить заказ" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Ресторан сейчас не принимает заказы. Попробуйте позже.",
    );
    expect(cartLine("Бао с грибами")).toBeVisible();
  });

  it("retries by hand with the same idempotency key", async () => {
    storeCart([{ menuItemId: "menu-bao", quantity: 1 }]);
    const keys: string[] = [];
    mockServer.use(
      publicGraphql.mutation<never, PlaceOrderVariables>(
        "PlaceOrder",
        ({ variables }) => {
          keys.push(variables.input.idempotencyKey);
          if (keys.length === 1) return HttpResponse.error();
        },
      ),
    );
    const user = await openCheckout();
    await user.click(screen.getByRole("button", { name: "Оформить заказ" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Не удалось отправить заказ/,
    );
    expect(keys).toHaveLength(1);
    await user.click(screen.getByRole("button", { name: "Отправить ещё раз" }));

    expect(
      await screen.findByRole("button", { name: /Кухня №8.*Оформлен/ }),
    ).toBeInTheDocument();
    expect(keys).toHaveLength(2);
    expect(keys[1]).toBe(keys[0]);
  });

  it("starts a new attempt when the cart changes after a failure", async () => {
    storeCart([{ menuItemId: "menu-bao", quantity: 1 }]);
    const keys: string[] = [];
    mockServer.use(
      publicGraphql.mutation<never, PlaceOrderVariables>(
        "PlaceOrder",
        ({ variables }) => {
          keys.push(variables.input.idempotencyKey);
          if (keys.length === 1) return HttpResponse.error();
        },
      ),
    );
    const user = await openCheckout();
    await user.click(screen.getByRole("button", { name: "Оформить заказ" }));
    await screen.findByRole("alert");
    await user.click(
      screen.getByRole("button", {
        name: "Увеличить количество: Бао с грибами",
      }),
    );
    await user.click(screen.getByRole("button", { name: "Отправить ещё раз" }));

    await screen.findByRole("button", { name: /Кухня №8.*Оформлен/ });
    expect(keys).toHaveLength(2);
    expect(keys[1]).not.toBe(keys[0]);
  });
});
