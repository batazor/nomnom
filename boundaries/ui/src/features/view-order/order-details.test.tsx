import { act, render, screen, within } from "@testing-library/react";
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
import { courierStepMs } from "../../shared/graphql/mocks/courier-tracks";
import { mockServer } from "../../shared/graphql/mocks/server";
import { liveStepMs } from "../../shared/graphql/mocks/store";
import { OrderDetails } from "./order-details";

const config = {
  publicApiUrl: "https://public.example.test",
  locale: "ru" as const,
  enableMocks: true,
  environment: "test",
};

const publicGraphql = graphql.link(/\/graphql$/);

beforeAll(() => mockServer.listen({ onUnhandledFrame: "error" }));
// The screens of a Market render in a chosen Market (ADR 41).
beforeEach(() => localStorage.setItem("nomnom.market.v1", "berlin"));
afterEach(() => {
  mockServer.resetHandlers();
  mockStore.reset();
  vi.useRealTimers();
});
afterAll(() => mockServer.close());

function renderOrder(orderId: string) {
  return render(
    <AppProviders config={config}>
      {/* The board around the details: page title and the panel heading. */}
      <main>
        <h1>Доска заказа</h1>
        <h2>Подробнее о заказе</h2>
        <OrderDetails orderId={orderId} />
      </main>
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

describe("view order", () => {
  it("shows a preparing order with progress, courier note and summary", async () => {
    renderOrder("order-preparing");

    expect(
      await screen.findByRole("heading", { level: 3, name: "Готовится" }),
    ).toBeInTheDocument();
    const current = screen
      .getAllByRole("listitem")
      .find((item) => item.getAttribute("aria-current") === "step");
    expect(current).toHaveTextContent("Готовится");
    // AC-795: the customer sees the name of the courier, not of the cook.
    expect(current).toHaveTextContent("Курьер Тимур назначен");
    expect(screen.getByText("Кухня №8")).toBeInTheDocument();
    expect(screen.getByText("Бао с грибами")).toBeInTheDocument();
    expect(screen.getByTestId("order-total")).toHaveTextContent(/1.?530/);
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
              placedAt: "2026-10-25T12:00:00Z",
              milestones: [
                { kind: "PLACED", occurredAt: "2026-10-25T12:00:00Z" },
              ],
              cancellation: null,
              expectedReadyAt: null,
              expectedPickupAt: null,
              expectedDeliveredAt: null,
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
      await screen.findByRole("heading", { level: 3, name: "Готовится" }),
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
      await screen.findByRole("heading", { level: 3, name: "Заказ отменён" }),
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
      screen.getByRole("heading", { level: 3, name: "Заказ оформлен" }),
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
      await screen.findByRole("heading", { level: 3, name: "Заказ отменён" }),
    ).toBeInTheDocument();
    expect(requestIds).toHaveLength(2);
    expect(requestIds[1]).toBe(requestIds[0]);
  });
});

describe("order updates", () => {
  it("follows order-live by subscription, without polling, until delivered", async () => {
    vi.useFakeTimers({
      shouldAdvanceTime: true,
      toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"],
    });
    const requests = countOrderRequests();
    renderOrder("order-live");

    expect(
      await screen.findByRole("heading", { level: 3, name: "Заказ оформлен" }),
    ).toBeInTheDocument();
    for (const label of [
      "Заказ оплачен",
      "Ресторан принял заказ",
      "Готовится",
      "Готов",
      "В пути",
      "Доставлен",
    ]) {
      await act(() => vi.advanceTimersByTimeAsync(liveStepMs));
      expect(
        await screen.findByRole("heading", { level: 3, name: label }),
      ).toBeInTheDocument();
    }
    const delivered = requests.value;

    await act(() => vi.advanceTimersByTimeAsync(120_000));
    expect(requests.value).toBe(delivered);
    // The first load and one refetch when each stream connects (ADR 36):
    // orderChanged, then courierMoved once the courier is assigned.
    expect(delivered).toBe(3);
  });
});

// FR-664, FR-669: the live map of the Virtual City while the courier is on
// the way.
describe("courier map", () => {
  function markerPosition() {
    return screen.getByTestId("courier-marker").getAttribute("transform");
  }

  it("AC-887: moves the courier by courierMoved, without polling", async () => {
    vi.useFakeTimers({
      shouldAdvanceTime: true,
      toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"],
    });
    const requests = countOrderRequests();
    renderOrder("order-picked-up");

    const map = await screen.findByRole("img", {
      name: "Курьер везёт заказ",
    });
    expect(map).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Где курьер" })).toBeVisible();
    const start = markerPosition();
    // The first load and the refetch when the streams connect (ADR 36).
    await vi.waitFor(() => expect(requests.value).toBeGreaterThanOrEqual(2));
    await act(() => vi.advanceTimersByTimeAsync(courierStepMs));
    const loads = requests.value;

    for (let step = 0; step < 3; step += 1) {
      await act(() => vi.advanceTimersByTimeAsync(courierStepMs));
    }

    await vi.waitFor(() => {
      expect(map.querySelector(".order-map-trail")).not.toBeNull();
      expect(markerPosition()).not.toBe(start);
    });
    expect(requests.value).toBe(loads);
  });

  it("heads to the restaurant before the pickup", async () => {
    renderOrder("order-preparing");

    expect(
      await screen.findByRole("img", { name: "Курьер едет в ресторан" }),
    ).toBeInTheDocument();
  });

  it("AC-888: shows no map for a delivered order or one without a courier", async () => {
    renderOrder("order-delivered");
    await screen.findByRole("heading", { level: 3, name: "Доставлен" });
    expect(screen.queryByRole("heading", { name: "Где курьер" })).toBeNull();
  });

  it("passes axe with the map", async () => {
    const { container } = renderOrder("order-picked-up");
    await screen.findByRole("img", { name: "Курьер везёт заказ" });

    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});

// AC-833, AC-834: the payment is a progress step, a declined one replaces it.
describe("payment step", () => {
  it("marks the paid step and keeps the restaurant next", async () => {
    renderOrder("order-paid");

    expect(
      await screen.findByRole("heading", { level: 3, name: "Заказ оплачен" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Оплата подтверждена")).toBeInTheDocument();
  });

  it("explains a declined payment instead of the progress", async () => {
    renderOrder("order-payment-declined");

    expect(await screen.findByText("Оплата не прошла")).toBeInTheDocument();
    expect(document.getElementById("order-progress-title")).toBeNull();
  });
});

describe("fulfillment eta", () => {
  function fakeClock() {
    vi.useFakeTimers({
      shouldAdvanceTime: true,
      toFake: [
        "setTimeout",
        "clearTimeout",
        "setInterval",
        "clearInterval",
        "Date",
      ],
    });
    // The fixed scenarios count from the faked now.
    mockStore.reset();
  }

  const time = String.raw`\d\d:\d\d`;
  // Attributes keep the no-break spaces of the messages; text queries see
  // them normalized to plain spaces.
  const nb = "\u00a0";

  function bar(name: string) {
    return screen.getByRole("progressbar", { name });
  }

  // The header line under the status, checked against its whole text.
  function header(text: RegExp) {
    const found = document.querySelector(".order-header-eta");
    if (!(found instanceof HTMLElement)) throw new Error("no header eta");
    expect(found).toHaveTextContent(text);
    return found;
  }

  function currentStep() {
    const found = document.querySelector("[aria-current='step']");
    if (!(found instanceof HTMLElement)) throw new Error("no current step");
    return found;
  }

  it("AC-772: grows the cooking bar of the active step locally, without requests", async () => {
    fakeClock();
    const requests = countOrderRequests();
    renderOrder("order-preparing");

    const cooking = await screen.findByRole("progressbar", { name: "Готовка" });
    expect(currentStep()).toContainElement(cooking);
    expect(screen.getAllByRole("progressbar")).toHaveLength(1);
    expect(cooking).toHaveAttribute("aria-valuemin", "0");
    expect(cooking).toHaveAttribute("aria-valuemax", "100");
    expect(cooking).toHaveAttribute(
      "aria-valuetext",
      expect.stringMatching(
        new RegExp(`^Осталось ~10${nb}мин, к${nb}${time}$`),
      ),
    );
    expect(cooking).toHaveAttribute("aria-valuenow", "50");
    expect(screen.getByText(`~10 мин`)).toBeVisible();
    // Pickup is the best known deadline of a cooking order with a courier.
    expect(
      header(new RegExp(`^Курьер заберёт заказ к ${time}$`)),
    ).toBeVisible();
    const loaded = requests.value;

    await act(() => vi.advanceTimersByTimeAsync(5 * 60_000));
    expect(bar("Готовка")).toHaveAttribute(
      "aria-valuetext",
      expect.stringMatching(/^Осталось ~5\u00a0мин/),
    );
    expect(bar("Готовка")).toHaveAttribute("aria-valuenow", "75");
    expect(requests.value).toBe(loaded);
  });

  it("counts down m:ss in the last five minutes, not in aria-valuetext", async () => {
    fakeClock();
    const requests = countOrderRequests();
    renderOrder("order-short");

    const delivery = await screen.findByRole("progressbar", {
      name: "Доставка",
    });
    expect(screen.getByText("3:00")).toBeVisible();
    expect(header(new RegExp(`^Привезём к ${time} · 3:00$`))).toBeVisible();
    const valueText = delivery.getAttribute("aria-valuetext");
    expect(valueText).toMatch(/^Осталось ~3\u00a0мин/);
    const loaded = requests.value;

    await act(() => vi.advanceTimersByTimeAsync(1_000));
    expect(screen.getByText("2:59")).toBeVisible();
    expect(bar("Доставка")).toHaveAttribute("aria-valuetext", valueText);

    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(screen.getByText("2:49")).toBeVisible();
    expect(requests.value).toBe(loaded);
  });

  it("shows a +N min chip for a few seconds when the deadline moves later", async () => {
    fakeClock();
    renderOrder("order-delayed");

    const cooking = await screen.findByRole("progressbar", { name: "Готовка" });
    expect(cooking.getAttribute("aria-valuetext")).toMatch(/^Осталось ~10/);
    const before = header(/^Будет готов к/).textContent;
    expect(screen.queryByText(`+6 мин`)).toBeNull();

    // The stream connects after the load; the kitchen delays one step later.
    await act(() => vi.advanceTimersByTimeAsync(liveStepMs + 500));
    expect(await screen.findByText(`+6 мин`)).toBeVisible();
    expect(
      screen.getByText(
        new RegExp(`^Срок сдвинулся на 6 мин, теперь к ${time}$`),
      ),
    ).toBeInTheDocument();
    expect(bar("Готовка").getAttribute("aria-valuetext")).toMatch(
      /^Осталось ~16/,
    );
    expect(header(/^Будет готов к/).textContent).not.toBe(before);

    await act(() => vi.advanceTimersByTimeAsync(5_000));
    expect(screen.queryByText(`+6 мин`)).toBeNull();
    expect(screen.queryByText(/^Срок сдвинулся/)).toBeNull();
  });

  it("says a missed deadline is late in the header and the bar", async () => {
    fakeClock();
    renderOrder("order-overdue");

    const cooking = await screen.findByRole("progressbar", { name: "Готовка" });
    expect(cooking).toHaveAttribute("aria-valuenow", "100");
    expect(cooking.getAttribute("aria-valuetext")).toMatch(
      new RegExp(`^Задерживается на ~3${nb}мин, ожидали к${nb}${time}$`),
    );
    expect(cooking.closest(".eta-bar")).toHaveAttribute(
      "data-state",
      "overdue",
    );
    const late = header(new RegExp(`^Задерживается · ожидали к ${time}$`));
    expect(late).toHaveAttribute("data-state", "overdue");

    await act(() => vi.advanceTimersByTimeAsync(60_000));
    expect(screen.getByText(`Задерживается на ~4 мин`)).toBeVisible();
  });

  it("moves the bar to the courier step after the pickup", async () => {
    fakeClock();
    renderOrder("order-picked-up");

    const delivery = await screen.findByRole("progressbar", {
      name: "Доставка",
    });
    expect(currentStep()).toContainElement(delivery);
    expect(delivery.getAttribute("aria-valuetext")).toMatch(/^Осталось ~10/);
    expect(screen.getAllByRole("progressbar")).toHaveLength(1);
    expect(header(new RegExp(`^Привезём к ${time} · ~10 мин$`))).toBeVisible();
  });

  it.each(["order-received", "order-delivered", "order-cancelled"])(
    "shows no bar and no header line without a deadline or after the end: %s",
    async (id) => {
      renderOrder(id);
      await screen.findByRole("heading", { level: 3 });
      expect(screen.queryByRole("progressbar")).toBeNull();
      expect(document.querySelector(".order-header-eta")).toBeNull();
      expect(screen.queryByText("Срок пока не назван")).toBeNull();
    },
  );

  it.each(["order-preparing", "order-short", "order-overdue"])(
    "passes axe with the eta of %s",
    async (id) => {
      const { container } = renderOrder(id);
      await screen.findByRole("progressbar");

      const result = await axe.run(container, {
        rules: { "color-contrast": { enabled: false } },
      });
      expect(result.violations).toEqual([]);
    },
  );
});

describe("order page quality", () => {
  it("is keyboard reachable and passes axe", async () => {
    const user = userEvent.setup();
    const { container } = renderOrder("order-received");
    await screen.findByRole("heading", { level: 3, name: "Заказ оформлен" });

    const cancel = screen.getByRole("button", { name: "Отменить заказ" });
    while (document.activeElement !== cancel) {
      await user.tab();
      if (document.activeElement === document.body) break;
    }
    expect(cancel).toHaveFocus();

    const progress = screen.getAllByRole("list")[0];
    expect(within(progress).getAllByRole("listitem")).toHaveLength(7);

    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});
