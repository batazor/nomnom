import axe from "axe-core";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse } from "msw";
import { graphql } from "msw/graphql";
import {
  afterAll,
  afterEach,
  beforeAll,
  describe,
  expect,
  it,
  vi,
} from "vitest";

import { AppProviders } from "../../app/providers";
import { AppRouter } from "../../app/router";
import { mockStore } from "../../shared/graphql/mocks/handlers";
import { mockServer } from "../../shared/graphql/mocks/server";
import { nextPollDelay } from "./use-order-polling";

const config = {
  publicGraphqlUrl: "https://public.example.test/graphql",
  locale: "ru" as const,
  enableMocks: true,
};

const publicGraphql = graphql.link(/\/graphql$/);

beforeAll(() => mockServer.listen({ onUnhandledFrame: "error" }));
afterEach(() => {
  mockServer.resetHandlers();
  mockStore.reset();
  vi.useRealTimers();
});
afterAll(() => mockServer.close());

function renderOrder(orderId: string) {
  return render(
    <AppProviders config={config}>
      <AppRouter initialEntries={[`/orders/${orderId}`]} />
    </AppProviders>,
  );
}

// Counts order requests and lets them through to the mock store.
function countOrderRequests() {
  const counter = { value: 0 };
  mockServer.use(
    publicGraphql.query("ViewOrder", () => {
      counter.value += 1;
    }),
  );
  return counter;
}

function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    value: state,
  });
  document.dispatchEvent(new Event("visibilitychange"));
}

describe("view order", () => {
  it("shows a preparing order with progress, courier note and summary", async () => {
    renderOrder("order-preparing");

    expect(
      await screen.findByRole("heading", { level: 1, name: "Готовится" }),
    ).toBeInTheDocument();
    const current = screen
      .getAllByRole("listitem")
      .find((item) => item.getAttribute("aria-current") === "step");
    expect(current).toHaveTextContent("Готовится");
    expect(current).toHaveTextContent("Курьер назначен");
    expect(screen.getByText("Кухня №8")).toBeInTheDocument();
    expect(screen.getByText("Бао с грибами")).toBeInTheDocument();
    expect(screen.getByTestId("order-total")).toHaveTextContent(/1.?530/);
    expect(screen.getByText("ул. Лесная, 12, кв. 47")).toBeInTheDocument();
    expect(screen.getByText("Назначен курьер")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Отменить заказ" }),
    ).not.toBeInTheDocument();
  });

  it("labels an item that is no longer in the menu", async () => {
    const money = { amount: "690.00", currency: "RUB" };
    mockServer.use(
      publicGraphql.query("ViewOrder", () =>
        HttpResponse.json({
          data: {
            order: {
              id: "order-old",
              status: "DELIVERED",
              endReason: null,
              restaurant: { id: "restaurant-1", name: "Кухня №8" },
              items: [
                {
                  menuItem: { id: "menu-gone", name: "" },
                  quantity: 1,
                  unitPrice: money,
                  lineTotal: money,
                },
              ],
              total: money,
              deliveryAddress: "ул. Лесная, 12",
              placedAt: "2026-10-25T12:00:00Z",
              milestones: [
                { kind: "PLACED", occurredAt: "2026-10-25T12:00:00Z" },
              ],
              cancellation: null,
            },
          },
        }),
      ),
    );
    renderOrder("order-old");

    expect(await screen.findByText("Позиция меню")).toBeVisible();
  });

  it("says the order is not found when the API returns null", async () => {
    renderOrder("order-unknown");
    expect(
      await screen.findByRole("heading", { name: "Заказ не найден" }),
    ).toBeInTheDocument();
  });

  it("offers recovery without technical details when the BFF fails", async () => {
    const user = userEvent.setup();
    mockServer.use(
      publicGraphql.query("ViewOrder", () =>
        HttpResponse.json({
          data: { order: null },
          errors: [
            { message: "rpc error: code = Unimplemented", path: ["order"] },
          ],
        }),
      ),
    );
    renderOrder("order-preparing");

    expect(
      await screen.findByRole("heading", {
        name: "Не удалось загрузить заказ",
      }),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Unimplemented|rpc/)).not.toBeInTheDocument();

    mockServer.resetHandlers();
    await user.click(screen.getByRole("button", { name: "Повторить" }));
    expect(
      await screen.findByRole("heading", { level: 1, name: "Готовится" }),
    ).toBeInTheDocument();
  });

  it.each([
    ["order-cancelled", "Вы отменили заказ"],
    ["order-restaurant-rejected", "Ресторан не смог принять заказ"],
    ["order-delivery-failed", "Доставка не удалась"],
  ])("explains why %s ended instead of showing progress", async (id, title) => {
    renderOrder(id);

    expect(await screen.findByRole("heading", { name: title })).toBeVisible();
    expect(screen.queryByRole("list", { name: "Ход заказа" })).toBeNull();
    expect(document.querySelector("[aria-current='step']")).toBeNull();
    expect(screen.getByRole("main")).not.toHaveTextContent(
      /возврат|вернём|CUSTOMER_CANCELLED|RESTAURANT_REJECTED|DELIVERY_FAILED/,
    );
  });

  it("requests cancellation inside the window and shows the new status", async () => {
    const user = userEvent.setup();
    renderOrder("order-received");

    expect(
      await screen.findByText(/Отменить можно до \d\d:\d\d/),
    ).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Отменить заказ" }));
    await user.click(screen.getByRole("button", { name: "Да, отменить" }));

    expect(
      await screen.findByRole("heading", { level: 1, name: "Заказ отменён" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Вы отменили заказ" }),
    ).toBeVisible();
  });

  it("keeps the order and the focus when the customer changes their mind", async () => {
    const user = userEvent.setup();
    renderOrder("order-received");

    await user.click(
      await screen.findByRole("button", { name: "Отменить заказ" }),
    );
    expect(
      screen.getByRole("group", { name: "Отменить заказ?" }),
    ).toBeVisible();
    expect(screen.getByRole("button", { name: "Не отменять" })).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(
      screen.getByRole("button", { name: "Отменить заказ" }),
    ).toHaveFocus();
    expect(
      screen.getByRole("heading", { level: 1, name: "Заказ принят" }),
    ).toBeInTheDocument();
  });

  it("hides the action when the backend says the window has closed", async () => {
    const user = userEvent.setup();
    renderOrder("order-received");

    await user.click(
      await screen.findByRole("button", { name: "Отменить заказ" }),
    );
    mockStore.setClock(() => new Date(Date.now() + 10 * 60_000));
    await user.click(screen.getByRole("button", { name: "Да, отменить" }));

    expect(
      await screen.findByText(
        "Отменить заказ уже нельзя: ресторан начал его выполнять.",
      ),
    ).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "Отменить заказ" }),
    ).not.toBeInTheDocument();
  });

  it("retries cancellation by hand with the same request id", async () => {
    const user = userEvent.setup();
    const requestIds: string[] = [];
    mockServer.use(
      publicGraphql.mutation("RequestOrderCancellation", ({ variables }) => {
        requestIds.push(variables.input.requestId);
        if (requestIds.length === 1) return HttpResponse.error();
      }),
    );
    renderOrder("order-received");

    await user.click(
      await screen.findByRole("button", { name: "Отменить заказ" }),
    );
    await user.click(screen.getByRole("button", { name: "Да, отменить" }));
    await user.click(
      await screen.findByRole("button", { name: "Повторить отмену" }),
    );

    expect(
      await screen.findByRole("heading", { level: 1, name: "Заказ отменён" }),
    ).toBeInTheDocument();
    expect(requestIds).toHaveLength(2);
    expect(requestIds[1]).toBe(requestIds[0]);
  });
});

describe("order polling", () => {
  it("backs off up to a minute", () => {
    expect([0, 1, 2, 3, 4].map(nextPollDelay)).toEqual([
      10_000, 20_000, 40_000, 60_000, 60_000,
    ]);
  });

  it("follows order-live every 10 seconds and stops when delivered", async () => {
    vi.useFakeTimers({
      shouldAdvanceTime: true,
      toFake: ["setTimeout", "clearTimeout"],
    });
    const requests = countOrderRequests();
    renderOrder("order-live");

    expect(
      await screen.findByRole("heading", { level: 1, name: "Заказ принят" }),
    ).toBeInTheDocument();
    for (const label of [
      "Ресторан подтвердил",
      "Готовится",
      "Готов",
      "В пути",
      "Доставлен",
    ]) {
      await act(() => vi.advanceTimersByTimeAsync(10_000));
      expect(
        await screen.findByRole("heading", { level: 1, name: label }),
      ).toBeInTheDocument();
    }
    const delivered = requests.value;

    await act(() => vi.advanceTimersByTimeAsync(120_000));
    expect(requests.value).toBe(delivered);
    expect(delivered).toBe(6);
  });

  it("pauses while the document is hidden and refreshes on return", async () => {
    vi.useFakeTimers({
      shouldAdvanceTime: true,
      toFake: ["setTimeout", "clearTimeout"],
    });
    const requests = countOrderRequests();
    renderOrder("order-preparing");
    await screen.findByRole("heading", { level: 1, name: "Готовится" });
    expect(requests.value).toBe(1);

    act(() => setVisibility("hidden"));
    await act(() => vi.advanceTimersByTimeAsync(60_000));
    expect(requests.value).toBe(1);

    act(() => setVisibility("visible"));
    await vi.waitFor(() => expect(requests.value).toBe(2));
  });
});

describe("order page quality", () => {
  it("fits a 390px viewport, is keyboard reachable and passes axe", async () => {
    const user = userEvent.setup();
    Object.defineProperty(window, "innerWidth", {
      value: 390,
      configurable: true,
    });
    const { container } = renderOrder("order-received");
    await screen.findByRole("heading", { level: 1, name: "Заказ принят" });

    expect(document.body.scrollWidth).toBeLessThanOrEqual(window.innerWidth);
    const cancel = screen.getByRole("button", { name: "Отменить заказ" });
    while (document.activeElement !== cancel) {
      await user.tab();
      if (document.activeElement === document.body) break;
    }
    expect(cancel).toHaveFocus();

    const progress = screen.getAllByRole("list")[0];
    expect(within(progress).getAllByRole("listitem")).toHaveLength(6);

    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});
