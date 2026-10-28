import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse } from "msw";
import { graphql } from "msw/graphql";
import {
  afterAll,
  afterEach,
  beforeAll,
  beforeEach,
  describe,
  expect,
  it,
} from "vitest";

import { AppProviders } from "../../app/providers";
import { AppRouter } from "../../app/router";
import { mockStore } from "../../shared/graphql/mocks/handlers";
import { mockServer } from "../../shared/graphql/mocks/server";

const config = {
  publicApiUrl: "https://public.example.test",
  locale: "ru" as const,
  enableMocks: true,
  environment: "test",
};

const kitchen = "restaurant-kitchen-8";
const cartKey = `nomnom.cart.v1.${kitchen}`;
const publicGraphql = graphql.link(/\/graphql$/);

beforeAll(() => mockServer.listen({ onUnhandledFrame: "error" }));
// The board opens only in a chosen Market (ADR 41).
beforeEach(() => localStorage.setItem("nomnom.market.v1", "berlin"));
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
    priority: string;
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
      await screen.findByRole("button", { name: /Кухня №8.*Оплачен/ }),
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
        priority: "STANDARD",
        idempotencyKey: expect.stringMatching(/^[0-9a-f-]{36}$/),
      },
    ]);
    expect(localStorage.getItem(cartKey)).toBeNull();
  });

  it("AC-864 AC-866 places a premium order with the fee in its total", async () => {
    // 2 × Том ям 690 ₽ + Бао 420 ₽ = 1 800 ₽; priority adds 99 ₽.
    storeCart([
      { menuItemId: "menu-tom-yum", quantity: 2 },
      { menuItemId: "menu-bao", quantity: 1 },
    ]);
    const requests = recordPlaceOrder();
    const user = await openCheckout();

    await user.click(
      screen.getByRole("checkbox", { name: /Приоритет — приготовим первым/ }),
    );
    expect(screen.getByText(/С приоритетом: 1.?899/)).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Оформить заказ" }));

    expect(
      await screen.findByRole("button", { name: /Кухня №8.*Оплачен/ }),
    ).toBeInTheDocument();
    expect(requests[0]?.priority).toBe("PREMIUM");
    await user.click(
      screen.getByRole("button", { name: "Подробнее о заказе" }),
    );
    expect(await screen.findByTestId("order-total")).toHaveTextContent(
      /1.?899/,
    );
    expect(screen.getByTestId("order-premium-fee")).toHaveTextContent(/99/);
    expect(screen.getAllByText("Приоритет").length).toBeGreaterThan(0);
  });

  it("AC-865 offers no priority where the restaurant has no fee", async () => {
    localStorage.setItem(
      "nomnom.cart.v1.restaurant-north-dumplings",
      JSON.stringify({ lines: [{ menuItemId: "menu-pelmeni", quantity: 1 }] }),
    );
    const requests = recordPlaceOrder();
    const user = userEvent.setup();
    render(
      <AppProviders config={config}>
        <AppRouter initialEntries={["/"]} />
      </AppProviders>,
    );
    await user.click(
      await screen.findByRole("button", { name: /^Пельменная «Север»/ }),
    );
    await screen.findByRole("heading", {
      level: 3,
      name: "Пельменная «Север»",
    });

    expect(
      screen.queryByRole("checkbox", { name: /Приоритет/ }),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Оформить заказ" }));
    expect(
      await screen.findByRole("button", {
        name: /Пельменная «Север».*Оплачен/,
      }),
    ).toBeInTheDocument();
    expect(requests[0]?.priority).toBe("STANDARD");
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
      await screen.findByRole("button", { name: /Кухня №8.*Оплачен/ }),
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

  it("asks to sign in when BFF refuses the token and keeps the cart", async () => {
    storeCart([{ menuItemId: "menu-bao", quantity: 1 }]);
    mockServer.use(
      publicGraphql.mutation("PlaceOrder", () =>
        HttpResponse.json({
          data: null,
          errors: [
            {
              message: "Failed to fetch from Subgraph 'bff'.",
              extensions: {
                errors: [
                  {
                    message: "authentication required",
                    extensions: { code: "Unauthenticated" },
                  },
                ],
              },
            },
          ],
        }),
      ),
    );
    const user = await openCheckout();
    await user.click(screen.getByRole("button", { name: "Оформить заказ" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Войдите, чтобы оформить заказ. Корзина сохранится.",
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
      await screen.findByRole("button", { name: /Кухня №8.*Оплачен/ }),
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

    await screen.findByRole("button", { name: /Кухня №8.*Оплачен/ });
    expect(keys).toHaveLength(2);
    expect(keys[1]).not.toBe(keys[0]);
  });
});
