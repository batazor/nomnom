import {
  act,
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
import { AppRouter } from "../../app/router";
import type { RuntimeConfig } from "../../shared/config/runtime-config";
import {
  courierPoolDeniedHandlers,
  mockStore,
} from "../../shared/graphql/mocks/handlers";
import { mockServer } from "../../shared/graphql/mocks/server";
import { parseBoardOrders } from "./board-orders";
import { CookPoolPanel } from "./cook-pool";

const config = {
  publicGraphqlUrl: "https://public.example.test/graphql",
  locale: "ru" as const,
  enableMocks: true,
  environment: "test",
};

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

function renderBoard(overrides: Partial<RuntimeConfig> = {}) {
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
      await within(column("Кухня")).findByText(
        "Выберите ресторан, чтобы открыть его кухню.",
      ),
    ).toBeVisible();
    expect(
      within(column("Кухня")).queryByRole("combobox"),
    ).not.toBeInTheDocument();
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

    // The courier goes online, takes the offer and delivers.
    const courier = column("Курьер");
    await user.click(
      within(courier).getByRole("button", { name: "Выйти на линию" }),
    );
    const offers = await within(courier).findByRole("list", {
      name: "Предложения",
    });
    // The placed order's offer is the newest.
    const offer = within(offers).getAllByRole("article").at(-1);
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

  it("places the order when the cart is dragged to the kitchen", async () => {
    const user = userEvent.setup();
    renderBoard();
    await user.click(await screen.findByRole("button", { name: /^Кухня №8/ }));
    await user.click(
      await screen.findByRole("button", {
        name: "Добавить в корзину: Манго и клейкий рис",
      }),
    );

    // The cart itself is dragged; there is no separate handle for it.
    const cart = screen
      .getByRole("region", { name: "Корзина" })
      .closest<HTMLElement>('[draggable="true"]');
    if (!cart) throw new Error("The filled cart is not draggable");
    fireEvent.dragStart(cart, { dataTransfer: dataTransfer() });
    expect(column("Кухня")).toHaveAttribute("data-drop", "ready");
    fireEvent.drop(column("Кухня"), { dataTransfer: dataTransfer() });

    expect(
      await screen.findByRole("button", { name: /Кухня №8.*Оформлен/ }),
    ).toBeInTheDocument();
  });

  it("carries an accepted order to the courier by dragging", async () => {
    const user = userEvent.setup();
    renderBoard();
    await user.click(await screen.findByRole("button", { name: /^Кухня №8/ }));
    await user.click(
      await screen.findByRole("button", {
        name: "Добавить в корзину: Манго и клейкий рис",
      }),
    );
    await user.click(screen.getByRole("button", { name: "Оформить заказ" }));
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

  it("grows the order details out of the timeline", async () => {
    const user = userEvent.setup();
    renderBoard();
    await placeOrder(user);

    await user.click(
      screen.getByRole("button", { name: "Подробнее о заказе" }),
    );
    const panel = await screen.findByRole("dialog");
    expect(
      await within(panel).findByRole("heading", { name: "Заказ оформлен" }),
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

  it("asks for the one demo sign-in before the kitchen and courier open", async () => {
    renderBoard({
      enableMocks: false,
      oidc: {
        issuer: "https://login.example.test/dex",
        clientId: "nomnom-ui",
        audience: "nomnom-bff",
        redirectUri: "https://board.example.test/auth/callback",
        demoEmailDomain: "nomnom.test",
        demoPassword: "nomnom",
      },
    });

    expect(
      await screen.findByRole("button", { name: "Войти в демо" }),
    ).toBeVisible();
    expect(
      within(column("Кухня")).getByText("Войдите, чтобы открыть колонку."),
    ).toBeVisible();
    expect(
      within(column("Курьер")).getByText("Войдите, чтобы открыть колонку."),
    ).toBeVisible();
  });

  it("has no detectable accessibility violations", async () => {
    const { container } = renderBoard();
    await screen.findByRole("button", { name: /^Кухня №8/ });

    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});

describe("kitchen cooking bar", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  // The fixed scenarios count from the faked now.
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
    mockStore.reset();
  }

  function countKitchenRequests() {
    const counter = { value: 0 };
    mockServer.use(
      publicGraphql.query("BoardKitchen", () => {
        counter.value += 1;
      }),
    );
    return counter;
  }

  async function openKitchen(user: ReturnType<typeof userEvent.setup>) {
    await user.click(await screen.findByRole("button", { name: /^Кухня №8/ }));
    return within(column("Кухня")).findByRole("list", {
      name: "Заказы кухни",
    });
  }

  const time = String.raw`\d\d:\d\d`;
  const nb = "\u00a0";

  it("counts the cooking of accepted orders locally, without requests", async () => {
    fakeClock();
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const requests = countKitchenRequests();
    renderBoard();
    const orders = await openKitchen(user);

    // order-preparing and order-delayed are due in 10 minutes, order-overdue
    // was due two and a half minutes ago; the RECEIVED order has no bar.
    const bars = await within(orders).findAllByRole("progressbar");
    expect(bars).toHaveLength(3);
    const received = orders.querySelector('.board-card[data-stage="RECEIVED"]');
    expect(received).not.toBeNull();
    expect(
      within(received as HTMLElement).queryByRole("progressbar"),
    ).toBeNull();
    expect(
      bars.map((bar) => bar.getAttribute("aria-valuetext")).sort(),
    ).toEqual([
      expect.stringMatching(
        new RegExp(`^Задерживается на ~3${nb}мин, ожидали к${nb}${time}$`),
      ),
      expect.stringMatching(
        new RegExp(`^Осталось ~10${nb}мин, к${nb}${time}$`),
      ),
      expect.stringMatching(
        new RegExp(`^Осталось ~10${nb}мин, к${nb}${time}$`),
      ),
    ]);
    for (const bar of bars) {
      expect(bar).toHaveAccessibleName(/^Готовка заказа от \d\d:\d\d$/);
    }
    expect(within(orders).getAllByText("Готово через ~10 мин")).toHaveLength(2);
    const late = within(orders).getByText("Задерживается на ~3 мин");
    expect(late.closest(".eta-bar")).toHaveAttribute("data-state", "overdue");
    const loaded = requests.value;

    // The last five minutes count down m:ss every second.
    await act(() => vi.advanceTimersByTimeAsync(5 * 60_000));
    expect(within(orders).getAllByText("Готово через 5:00")).toHaveLength(2);
    await act(() => vi.advanceTimersByTimeAsync(1_000));
    expect(within(orders).getAllByText("Готово через 4:59")).toHaveLength(2);
    expect(within(orders).getByText("Задерживается на ~8 мин")).toBeVisible();
    expect(requests.value).toBe(loaded);
  });

  it("shows the bar from acceptance until the order is ready", async () => {
    const user = userEvent.setup();
    renderBoard();
    await placeOrder(user);

    const card = await waitFor(selectedKitchenCard);
    expect(within(card).queryByRole("progressbar")).toBeNull();
    await user.click(within(card).getByRole("button", { name: "Принять" }));
    // The mock kitchen names readiness 15 minutes after acceptance.
    expect(
      await within(selectedKitchenCard()).findByRole("progressbar"),
    ).toHaveAttribute(
      "aria-valuetext",
      expect.stringMatching(
        new RegExp(`^Осталось ~15${nb}мин, к${nb}${time}$`),
      ),
    );
    expect(
      within(selectedKitchenCard()).getByText("Готово через ~15 мин"),
    ).toBeVisible();

    for (const action of ["Начать готовить", "Собран"]) {
      await user.click(
        await within(selectedKitchenCard()).findByRole("button", {
          name: action,
        }),
      );
      expect(
        await within(selectedKitchenCard()).findByRole("progressbar"),
      ).toBeInTheDocument();
    }
    await user.click(
      await within(selectedKitchenCard()).findByRole("button", {
        name: "Готов к выдаче",
      }),
    );
    expect(
      await within(selectedKitchenCard()).findByText("Ждёт курьера"),
    ).toBeVisible();
    expect(within(selectedKitchenCard()).queryByRole("progressbar")).toBeNull();
  });

  it("passes axe with the cooking bars", async () => {
    const user = userEvent.setup();
    const { container } = renderBoard();
    const orders = await openKitchen(user);
    await within(orders).findAllByRole("progressbar");

    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});

describe("cook and courier pools", () => {
  async function openKitchen(user: ReturnType<typeof userEvent.setup>) {
    await user.click(await screen.findByRole("button", { name: /^Кухня №8/ }));
    return within(column("Кухня")).findByRole("region", { name: "Повара" });
  }

  it("AC-796 shows the line, the load and the queue of the kitchen", async () => {
    const user = userEvent.setup();
    renderBoard();

    const cooks = await openKitchen(user);

    expect(within(cooks).getByText(/Линия 5 из 6/)).toBeVisible();
    expect(within(cooks).getByText(/занято \d+\/5/)).toBeVisible();
    expect(within(cooks).getByText(/В очереди: \d+/)).toBeVisible();
    expect(
      within(cooks).getByRole("meter", { name: "Загрузка линии: Повара" }),
    ).toBeInTheDocument();
    const members = within(cooks).getByRole("list", {
      name: "Повара ресторана",
    });
    expect(within(members).getByText("Марат")).toBeVisible();
    expect(within(members).getByText("не на линии")).toBeVisible();
  });

  it("hires a cook off line and puts them on line", async () => {
    const user = userEvent.setup();
    renderBoard();
    const cooks = await openKitchen(user);

    await user.type(within(cooks).getByLabelText("Имя повара"), "Тимур");
    await user.click(
      within(cooks).getByRole("button", { name: "Добавить повара" }),
    );
    await user.click(
      await within(cooks).findByRole("button", { name: "На линию: Тимур" }),
    );

    expect(await within(cooks).findByText(/Линия 6 из 7/)).toBeVisible();
    expect(within(cooks).getByLabelText("Имя повара")).toHaveValue("");
  });

  it("AC-797 warns when no cook is on line and orders wait", async () => {
    render(
      <AppProviders config={config}>
        <CookPoolPanel
          orderTimes={new Map()}
          pool={{
            size: 1,
            onLine: 0,
            busy: 0,
            queued: 2,
            cooks: [
              {
                id: "cook-1",
                name: "Марат",
                onLine: false,
                currentKitchenOrderId: null,
              },
            ],
          }}
          restaurantId="restaurant-1"
        />
      </AppProviders>,
    );

    expect(screen.getByRole("alert")).toHaveTextContent(
      "На линии нет поваров — заказы ждут в очереди.",
    );
  });

  it("AC-800 names the cook of an order in preparation", async () => {
    const user = userEvent.setup();
    renderBoard();
    await placeOrder(user);

    for (const action of ["Принять", "Начать готовить"]) {
      const card = await waitFor(selectedKitchenCard);
      await user.click(within(card).getByRole("button", { name: action }));
    }

    expect(
      await within(selectedKitchenCard()).findByText(/^Готовит: /),
    ).toBeVisible();
  });

  it("adds a courier to the Courier Pool of a dispatcher", async () => {
    const user = userEvent.setup();
    renderBoard();
    // The board renders once the Market list is loaded.
    const courierColumn = await screen.findByRole("region", { name: "Курьер" });
    const couriers = await within(courierColumn).findByRole("region", {
      name: "Курьеры",
    });
    expect(within(couriers).getByText(/Линия 1 из 3/)).toBeVisible();
    expect(within(couriers).getByText(/Ждут курьера: \d+/)).toBeVisible();

    await user.type(within(couriers).getByLabelText("Имя курьера"), "Сергей");
    await user.click(
      within(couriers).getByRole("button", { name: "Добавить курьера" }),
    );

    expect(await within(couriers).findByText("Сергей")).toBeVisible();
    expect(await within(couriers).findByText(/Линия 2 из 4/)).toBeVisible();
  });

  it("AC-798 hides the Courier Pool from a user who is no dispatcher", async () => {
    mockServer.use(...courierPoolDeniedHandlers);
    renderBoard();
    await screen.findByRole("region", { name: "Курьер" });

    expect(
      await within(column("Курьер")).findByText("Не на линии"),
    ).toBeVisible();
    expect(
      within(column("Курьер")).queryByRole("region", { name: "Курьеры" }),
    ).not.toBeInTheDocument();
  });

  it("passes axe with both pools", async () => {
    const user = userEvent.setup();
    const { container } = renderBoard();
    await openKitchen(user);
    await within(column("Курьер")).findByRole("region", { name: "Курьеры" });

    const results = await axe.run(container);
    expect(results.violations).toEqual([]);
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
});
