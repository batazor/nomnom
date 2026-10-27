import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
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
  vi,
} from "vitest";

import { AppProviders } from "../../app/providers";
import { mockStore } from "../../shared/graphql/mocks/handlers";
import { mockServer } from "../../shared/graphql/mocks/server";
import { cartStorageKey } from "./cart";
import { RestaurantOrdering } from "./restaurant-ordering";

const config = {
  publicApiUrl: "https://public.example.test",
  locale: "ru" as const,
  enableMocks: true,
  environment: "test",
};

const kitchen = "restaurant-kitchen-8";
const publicGraphql = graphql.link(/\/graphql$/);

beforeAll(() => mockServer.listen({ onUnhandledFrame: "error" }));
// The screens of a Market render in a chosen Market (ADR 41).
beforeEach(() => localStorage.setItem("nomnom.market.v1", "berlin"));
afterEach(() => {
  mockServer.resetHandlers();
  mockStore.reset();
  localStorage.clear();
  vi.restoreAllMocks();
});
afterAll(() => mockServer.close());

function renderRestaurant(restaurantId = kitchen) {
  return render(
    <AppProviders config={config}>
      {/* The board around the column: page title and the column heading. */}
      <main>
        <h1>Доска заказа</h1>
        <h2>Покупатель</h2>
        <RestaurantOrdering
          key={restaurantId}
          onPlaced={() => {}}
          restaurantId={restaurantId}
        />
      </main>
    </AppProviders>,
  );
}

function cart() {
  return screen.getByRole("region", { name: "Корзина" });
}

describe("view restaurant", () => {
  it("shows the menu with prices and an empty cart", async () => {
    renderRestaurant();

    expect(
      await screen.findByRole("heading", { level: 3, name: "Кухня №8" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 5, name: "Том ям с креветками" }),
    ).toBeVisible();
    expect(screen.getAllByText(/690/)).not.toHaveLength(0);
    expect(within(cart()).getByText(/Пока пусто/)).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "Оформить заказ" }),
    ).not.toBeInTheDocument();
  });

  it("adds items to the cart with an estimated subtotal", async () => {
    const user = userEvent.setup();
    renderRestaurant();

    const addBao = await screen.findByRole("button", {
      name: "Добавить в корзину: Бао с грибами",
    });
    await user.click(addBao);
    await user.click(addBao);
    await user.click(
      screen.getByRole("button", {
        name: "Добавить в корзину: Том ям с креветками",
      }),
    );

    expect(screen.getByText("В корзине: 2")).toBeVisible();
    expect(
      within(cart()).getByRole("status", { name: "Бао с грибами, количество" }),
    ).toHaveTextContent("2");
    expect(within(cart()).getByText("Предварительно")).toBeVisible();
    expect(screen.getByTestId("cart-subtotal")).toHaveTextContent(/1.?530/);
    await user.hover(
      within(cart()).getByRole("button", { name: "Справка: Предварительно" }),
    );
    expect(
      within(cart()).getByRole("tooltip", {
        name: /Точную сумму ресторан рассчитает/,
      }),
    ).toBeVisible();
  });

  it("changes quantities and removes lines", async () => {
    const user = userEvent.setup();
    renderRestaurant();

    await user.click(
      await screen.findByRole("button", {
        name: "Добавить в корзину: Бао с грибами",
      }),
    );
    await user.click(
      screen.getByRole("button", {
        name: "Увеличить количество: Бао с грибами",
      }),
    );
    expect(screen.getByTestId("cart-subtotal")).toHaveTextContent(/840/);

    await user.click(
      screen.getByRole("button", {
        name: "Уменьшить количество: Бао с грибами",
      }),
    );
    expect(screen.getByTestId("cart-subtotal")).toHaveTextContent(/420/);

    await user.click(
      screen.getByRole("button", { name: "Убрать из корзины: Бао с грибами" }),
    );
    expect(within(cart()).getByText(/Пока пусто/)).toBeVisible();
    expect(screen.getByRole("heading", { name: "Корзина" })).toHaveFocus();
  });

  it("shows an unavailable item but does not let it be added", async () => {
    renderRestaurant();

    expect(
      await screen.findByRole("heading", { level: 5, name: "Гёдза с уткой" }),
    ).toBeVisible();
    const add = screen.getByRole("button", {
      name: "Добавить в корзину: Гёдза с уткой",
    });
    expect(add).toBeDisabled();
    expect(add).toHaveAccessibleDescription("Нет в наличии");
  });

  it("keeps the cart of each restaurant across visits", async () => {
    const user = userEvent.setup();
    const first = renderRestaurant();
    await user.click(
      await screen.findByRole("button", {
        name: "Добавить в корзину: Манго и клейкий рис",
      }),
    );
    expect(
      JSON.parse(localStorage.getItem(cartStorageKey(kitchen)) ?? "null"),
    ).toEqual({ lines: [{ menuItemId: "menu-mango", quantity: 1 }] });
    first.unmount();

    renderRestaurant();
    expect(await screen.findByText("В корзине: 1")).toBeVisible();

    renderRestaurant("restaurant-north-dumplings");
    expect(
      await screen.findByRole("heading", { name: "Пельменная «Север»" }),
    ).toBeVisible();
    expect(screen.getAllByText(/Пока пусто/)).toHaveLength(1);
  });

  it("drops stored lines of items that left the menu", async () => {
    localStorage.setItem(
      cartStorageKey(kitchen),
      JSON.stringify({
        lines: [
          { menuItemId: "menu-retired", quantity: 1 },
          { menuItemId: "menu-bao", quantity: 2 },
        ],
      }),
    );
    renderRestaurant();

    expect(await screen.findByText("В корзине: 2")).toBeVisible();
    expect(
      JSON.parse(localStorage.getItem(cartStorageKey(kitchen)) ?? "null"),
    ).toEqual({ lines: [{ menuItemId: "menu-bao", quantity: 2 }] });
  });

  it("works when the browser storage is unavailable", async () => {
    const user = userEvent.setup();
    // Only the cart is blocked: the Market of the screen stays chosen.
    vi.spyOn(Storage.prototype, "getItem").mockImplementation((key) => {
      if (key === "nomnom.market.v1") return "berlin";
      throw new DOMException("blocked", "SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("full", "QuotaExceededError");
    });
    renderRestaurant();

    await user.click(
      await screen.findByRole("button", {
        name: "Добавить в корзину: Бао с грибами",
      }),
    );
    expect(screen.getByTestId("cart-subtotal")).toHaveTextContent(/420/);
  });

  it("AC-848 shows a longer wait and the expected minutes, ordering stays open", async () => {
    const user = userEvent.setup();
    renderRestaurant();

    expect(await screen.findByText("Повышенное время ожидания")).toBeVisible();
    await user.click(
      screen.getByRole("button", {
        name: "Добавить в корзину: Бао с грибами",
      }),
    );

    expect(within(cart()).getByText("Ожидание ≈42 мин")).toBeVisible();
    expect(
      within(cart()).getByRole("button", { name: "Оформить заказ" }),
    ).toBeEnabled();
  });

  it("FR-648 shows no badge for a kitchen at its usual pace", async () => {
    renderRestaurant("restaurant-north-dumplings");

    expect(
      await screen.findByRole("heading", {
        level: 3,
        name: "Пельменная «Север»",
      }),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("Повышенное время ожидания"),
    ).not.toBeInTheDocument();
  });

  it("does not take orders while the restaurant is closed", async () => {
    renderRestaurant("restaurant-night-bakery");

    expect(
      await screen.findByText(/Ресторан сейчас не принимает заказы/),
    ).toBeVisible();
    expect(
      screen.getByRole("button", {
        name: "Добавить в корзину: Круассан с миндалём",
      }),
    ).toBeDisabled();
  });

  it("says when the menu is not published yet", async () => {
    renderRestaurant("restaurant-corner-coffee");
    expect(
      await screen.findByText("Ресторан ещё не опубликовал меню."),
    ).toBeVisible();
  });

  it("says the restaurant is not found when the API returns null", async () => {
    renderRestaurant("restaurant-unknown");
    expect(
      await screen.findByRole("heading", { name: "Ресторан не найден" }),
    ).toBeVisible();
  });

  it("offers recovery when the menu cannot be loaded", async () => {
    const user = userEvent.setup();
    mockServer.use(
      publicGraphql.query("ViewRestaurant", () => HttpResponse.error(), {
        once: true,
      }),
    );
    renderRestaurant();

    await user.click(await screen.findByRole("button", { name: "Повторить" }));
    expect(
      await screen.findByRole("heading", { level: 3, name: "Кухня №8" }),
    ).toBeInTheDocument();
  });
});

describe("restaurant page quality", () => {
  it("is keyboard reachable and passes axe", async () => {
    const user = userEvent.setup();
    const { container } = renderRestaurant();
    const add = await screen.findByRole("button", {
      name: "Добавить в корзину: Том ям с креветками",
    });

    while (document.activeElement !== add) {
      await user.tab();
      if (document.activeElement === document.body) break;
    }
    expect(add).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(screen.getByText("В корзине: 1")).toBeVisible();

    expect(document.querySelectorAll('[data-ui-primary="true"]')).toHaveLength(
      1,
    );
    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});
