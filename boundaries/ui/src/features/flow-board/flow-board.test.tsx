import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { HttpResponse } from "msw";
import { graphql } from "msw/graphql";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";

import { AppProviders } from "../../app/providers";
import { AppRouter } from "../../app/router";
import { mockStore } from "../../shared/graphql/mocks/handlers";
import { mockServer } from "../../shared/graphql/mocks/server";
import { parseRoleSession } from "../../shared/session/roles";
import { parseBoardOrders } from "./board-orders";

const config = {
  publicGraphqlUrl: "https://public.example.test/graphql",
  locale: "ru" as const,
  enableMocks: true,
  environment: "test",
};

const publicGraphql = graphql.link(/\/graphql$/);

beforeAll(() => mockServer.listen({ onUnhandledFrame: "error" }));
afterEach(() => {
  mockServer.resetHandlers();
  mockStore.reset();
  localStorage.clear();
});
afterAll(() => mockServer.close());

function renderBoard(overrides: Partial<typeof config> = {}) {
  return render(
    <AppProviders config={{ ...config, ...overrides }}>
      <AppRouter initialEntries={["/"]} />
    </AppProviders>,
  );
}

function column(name: string) {
  return screen.getByRole("region", { name });
}

async function placeOrder(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole("button", { name: /^Кухня №8/ }));
  await user.click(
    await screen.findByRole("button", {
      name: "Добавить в корзину: Манго и клейкий рис",
    }),
  );
  await user.type(
    screen.getByRole("textbox", { name: "Адрес доставки" }),
    "ул. Морская, 4",
  );
  await user.click(screen.getByRole("button", { name: "Оформить заказ" }));
  return screen.findByRole("button", { name: /Кухня №8.*Оформлен/ });
}

// jsdom has no DataTransfer; the board only marks and reads the drag type.
function dataTransfer() {
  const data = new Map<string, string>();
  return {
    effectAllowed: "all",
    dropEffect: "none",
    setData: (type: string, value: string) => data.set(type, value),
    getData: (type: string) => data.get(type) ?? "",
  };
}

// The kitchen card of the order the board highlights.
function selectedKitchenCard() {
  const card = column("Кухня").querySelector<HTMLElement>(
    '.board-card[data-selected="true"]',
  );
  if (!card) throw new Error("No highlighted kitchen order");
  return card;
}

describe("flow board", () => {
  it("shows the customer, the kitchen and the courier on one screen", async () => {
    renderBoard();

    expect(
      await screen.findByRole("heading", { level: 2, name: "Покупатель" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 2, name: "Кухня" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 2, name: "Курьер" }),
    ).toBeInTheDocument();
    expect(
      await within(column("Кухня")).findByRole("combobox", {
        name: "Ресторан",
      }),
    ).toHaveValue("restaurant-kitchen-8");
    expect(
      await within(column("Курьер")).findByText("Не на линии"),
    ).toBeVisible();
  });

  it("carries one order from the cart to the door", async () => {
    const user = userEvent.setup();
    renderBoard();

    const chip = await placeOrder(user);
    expect(chip).toHaveAttribute("aria-pressed", "true");
    expect(
      await screen.findByText("Дальше: Кухня — «Принят кухней»"),
    ).toBeVisible();

    // The kitchen moves the new order through its stages.
    for (const action of [
      "Принять",
      "Начать готовить",
      "Собран",
      "Готов к выдаче",
    ]) {
      const card = await waitFor(selectedKitchenCard);
      await user.click(within(card).getByRole("button", { name: action }));
    }
    expect(
      await within(selectedKitchenCard()).findByText("Ждёт курьера"),
    ).toBeVisible();

    // The courier goes online, takes the offer to this address and delivers.
    const courier = column("Курьер");
    await user.click(
      within(courier).getByRole("button", { name: "Выйти на линию" }),
    );
    const offers = await within(courier).findByRole("list", {
      name: "Предложения",
    });
    const offer = within(offers)
      .getAllByRole("article")
      .find((candidate) => candidate.textContent?.includes("ул. Морская, 4"));
    if (!offer) throw new Error("No offer for the placed order");
    await user.click(
      within(offer).getByRole("button", { name: "Взять заказ" }),
    );
    await user.click(
      await within(courier).findByRole("button", { name: "Забрал заказ" }),
    );
    await user.click(
      await within(courier).findByRole("button", { name: "Доставил" }),
    );

    expect(
      await screen.findByText("Заказ доставлен.", {}, { timeout: 5_000 }),
    ).toBeVisible();
    expect(within(courier).getByText("На линии")).toBeVisible();
  }, 15_000);

  it("carries the order by dragging: cart to kitchen, kitchen to courier", async () => {
    const user = userEvent.setup();
    renderBoard();
    await user.click(await screen.findByRole("button", { name: /^Кухня №8/ }));
    await user.click(
      await screen.findByRole("button", {
        name: "Добавить в корзину: Манго и клейкий рис",
      }),
    );
    await user.type(
      screen.getByRole("textbox", { name: "Адрес доставки" }),
      "ул. Морская, 4",
    );

    // The cart ticket dropped on the kitchen places the order.
    const ticket = screen.getByText("Перетащите заказ на кухню");
    fireEvent.dragStart(ticket, { dataTransfer: dataTransfer() });
    expect(column("Кухня")).toHaveAttribute("data-drop", "ready");
    fireEvent.drop(column("Кухня"), { dataTransfer: dataTransfer() });
    await screen.findByRole("button", { name: /Кухня №8.*Оформлен/ });

    const accept = await waitFor(selectedKitchenCard);
    await user.click(within(accept).getByRole("button", { name: "Принять" }));

    // The accepted order dropped on the courier: online and taken.
    const card = await waitFor(() => {
      const found = selectedKitchenCard();
      expect(found).toHaveAttribute("draggable", "true");
      return found;
    });
    const courier = column("Курьер");
    fireEvent.dragStart(card, { dataTransfer: dataTransfer() });
    const target = courier.querySelector<HTMLElement>("[data-drop]");
    if (!target) throw new Error("Courier column does not accept the order");
    fireEvent.drop(target, { dataTransfer: dataTransfer() });

    expect(await within(courier).findByText("Текущая доставка")).toBeVisible();
    expect(within(courier).getByText("На доставке")).toBeVisible();
  });

  it("opens the order details in a side panel", async () => {
    const user = userEvent.setup();
    renderBoard();
    await placeOrder(user);

    await user.click(
      screen.getByRole("button", { name: "Подробнее о заказе" }),
    );
    const panel = await screen.findByRole("dialog");
    expect(
      await within(panel).findByRole("heading", { name: "Заказ принят" }),
    ).toBeVisible();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("keeps the placed orders across visits", async () => {
    const user = userEvent.setup();
    const first = renderBoard();
    await placeOrder(user);
    first.unmount();

    renderBoard();
    expect(
      await screen.findByRole("button", { name: /Кухня №8.*Оформлен/ }),
    ).toBeInTheDocument();
  });

  it("explains a kitchen command that came too late", async () => {
    const user = userEvent.setup();
    renderBoard();
    await placeOrder(user);
    mockServer.use(
      publicGraphql.mutation(
        "ApplyKitchenAction",
        () =>
          HttpResponse.json({
            data: {
              applyKitchenAction: {
                order: null,
                problem: "ORDER_STAGE_CONFLICT",
              },
            },
          }),
        { once: true },
      ),
    );

    const card = await waitFor(selectedKitchenCard);
    await user.click(within(card).getByRole("button", { name: "Принять" }));
    expect(
      await within(column("Кухня")).findByText(
        "Заказ уже на другом этапе — список обновлён.",
      ),
    ).toBeVisible();
  });

  it("keeps the catalog when the test sign-ins are unavailable", async () => {
    renderBoard({ enableMocks: false });

    expect(
      await screen.findByText(/Нет доступа за кухню и курьера/),
    ).toBeVisible();
    expect(
      within(column("Кухня")).getByText(
        "Колонка станет доступна, когда загрузятся тестовые входы.",
      ),
    ).toBeVisible();
    expect(
      await screen.findByRole("button", { name: /^Кухня №8/ }),
    ).toBeInTheDocument();
  });

  it("keeps three columns on a phone without page scroll and passes axe", async () => {
    Object.defineProperty(window, "innerWidth", {
      value: 390,
      configurable: true,
    });
    const { container } = renderBoard();
    await screen.findByRole("button", { name: /^Кухня №8/ });

    expect(
      screen.getAllByRole("heading", { level: 2 }).length,
    ).toBeGreaterThanOrEqual(3);
    expect(document.body.scrollWidth).toBeLessThanOrEqual(window.innerWidth);

    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});

describe("board orders", () => {
  it("drops orders BFF no longer knows instead of loading them forever", async () => {
    localStorage.setItem(
      "nomnom.board.orders.v1",
      JSON.stringify(["order-gone", "order-preparing"]),
    );
    renderBoard();

    expect(
      await screen.findByRole("button", { name: /Кухня №8.*Готовится/ }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(
        parseBoardOrders(localStorage.getItem("nomnom.board.orders.v1")),
      ).toEqual(["order-preparing"]),
    );
    expect(screen.queryByText("Загружаем заказ")).toBeNull();
  });
});

describe("board state", () => {
  it("reads only order ids from storage", () => {
    expect(parseBoardOrders('["a", 1, "b"]')).toEqual(["a", "b"]);
    expect(parseBoardOrders("{")).toEqual([]);
    expect(parseBoardOrders(null)).toEqual([]);
  });

  it("reads the dev token issuer response", () => {
    const session = parseRoleSession({
      tokens: { customer: "c", owner: "o", kitchen: "k", courier: "r" },
      expiresAt: "2026-10-25T20:00:00Z",
      restaurants: [{ id: "r1", name: "Пекарня" }, { id: 2 }],
    });
    expect(session.tokens.kitchen).toBe("k");
    expect(session.expiresAt).toBe(Date.parse("2026-10-25T20:00:00Z"));
    expect(session.restaurants).toEqual([{ id: "r1", name: "Пекарня" }]);
    expect(
      parseRoleSession({
        tokens: { customer: "c", owner: "o", kitchen: "k", courier: "r" },
        expiresAt: 1_792_958_400,
      }).expiresAt,
    ).toBe(1_792_958_400_000);
    expect(() => parseRoleSession({ tokens: { customer: "c" } })).toThrow(
      /owner/,
    );
  });
});
