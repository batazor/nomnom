import { render, screen } from "@testing-library/react";
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
} from "vitest";

import { AppProviders } from "../../app/providers";
import { mockStore } from "../../shared/graphql/mocks/handlers";
import { mockServer } from "../../shared/graphql/mocks/server";
import { bffError } from "../../shared/graphql/mocks/store";
import { RestaurantOpening } from "./restaurant-opening";

const config = {
  publicApiUrl: "https://public.example.test",
  locale: "ru" as const,
  enableMocks: true,
  environment: "test",
};

const kitchen = "restaurant-kitchen-8";
const bakery = "restaurant-night-bakery";
const publicGraphql = graphql.link(/\/graphql$/);

beforeAll(() => mockServer.listen({ onUnhandledFrame: "error" }));
// The screens of a Market render in a chosen Market (ADR 41).
beforeEach(() => localStorage.setItem("nomnom.market.v1", "berlin"));
afterEach(() => {
  mockServer.resetHandlers();
  mockStore.reset();
});
afterAll(() => mockServer.close());

// Opening state the board read for the restaurant, like myRestaurants does.
const openingOf: Record<string, boolean> = { [kitchen]: true, [bakery]: false };

function renderManage(restaurantId = kitchen) {
  return render(
    <AppProviders config={config}>
      <main>
        <RestaurantOpening
          restaurant={{
            id: restaurantId,
            acceptingOrders: openingOf[restaurantId] ?? true,
          }}
        />
      </main>
    </AppProviders>,
  );
}

function feedback() {
  return document.querySelector(".manage-feedback");
}

describe("manage restaurant", () => {
  // AC-755: the screen shows the opening state and changes it.
  it("shows an open restaurant and closes it", async () => {
    const user = userEvent.setup();
    renderManage();

    expect(await screen.findByText("Открыт")).toBeVisible();

    await user.click(
      screen.getByRole("switch", { name: "Приём заказов", checked: true }),
    );

    expect(
      await screen.findByRole("switch", {
        name: "Приём заказов",
        checked: false,
      }),
    ).toBeEnabled();
    expect(screen.getByText("Закрыт")).toBeVisible();
    expect(feedback()).toHaveTextContent("Ресторан закрыт для новых заказов.");
  });

  it("opens a closed restaurant, which then appears in the catalog", async () => {
    const user = userEvent.setup();
    renderManage(bakery);

    await user.click(
      await screen.findByRole("switch", {
        name: "Приём заказов",
        checked: false,
      }),
    );

    expect(
      await screen.findByRole("switch", {
        name: "Приём заказов",
        checked: true,
      }),
    ).toBeInTheDocument();
    expect(feedback()).toHaveTextContent("Ресторан открыт и принимает заказы.");

    const restaurants = mockStore.rootValue.restaurants({ first: 100 });
    expect(restaurants.edges.map((edge) => edge.node.id)).toContain(bakery);
  });

  // AC-755: one command per press while it is pending.
  it("keeps the action pending and sends one command per press", async () => {
    const user = userEvent.setup();
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    let sent = 0;
    mockServer.use(
      publicGraphql.mutation("CloseRestaurant", async () => {
        sent += 1;
        await gate;
        // Falls through to the schema-backed handler.
      }),
    );
    renderManage();

    const close = await screen.findByRole("switch", {
      name: "Приём заказов",
      checked: true,
    });
    await user.click(close);
    expect(await screen.findByText("Закрываем ресторан…")).toBeVisible();
    expect(
      screen.getByRole("region", { name: "Приём заказов" }),
    ).toHaveAttribute("aria-busy", "true");
    await user.click(close);
    expect(sent).toBe(1);

    release();
    expect(
      await screen.findByRole("switch", {
        name: "Приём заказов",
        checked: false,
      }),
    ).toBeInTheDocument();
  });

  it("tells a repeat that changed nothing", async () => {
    const user = userEvent.setup();
    mockServer.use(
      publicGraphql.mutation(
        "CloseRestaurant",
        () =>
          HttpResponse.json({
            data: {
              closeRestaurant: {
                __typename: "RestaurantOpeningPayload",
                changed: false,
                restaurant: {
                  __typename: "RestaurantSummary",
                  id: kitchen,
                  name: "Кухня №8",
                  acceptingOrders: false,
                },
              },
            },
          }),
        { once: true },
      ),
    );
    renderManage();

    await user.click(
      await screen.findByRole("switch", {
        name: "Приём заказов",
        checked: true,
      }),
    );

    expect(
      await screen.findByText("Ресторан уже был закрыт."),
    ).toBeInTheDocument();
  });

  // AC-756: a refusal of restaurants keeps the state and explains it.
  it("explains a missing right without changing the state", async () => {
    const user = userEvent.setup();
    mockServer.use(
      publicGraphql.mutation(
        "CloseRestaurant",
        () =>
          HttpResponse.json({
            data: null,
            errors: [bffError("PermissionDenied", "refused").toJSON()],
          }),
        { once: true },
      ),
    );
    renderManage();

    await user.click(
      await screen.findByRole("switch", {
        name: "Приём заказов",
        checked: true,
      }),
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "У вас нет прав управлять этим рестораном.",
    );
    expect(screen.getByText("Открыт")).toBeVisible();
    expect(
      screen.getByRole("switch", { name: "Приём заказов", checked: true }),
    ).toBeEnabled();
  });

  it.each([
    ["Unauthenticated", "Войдите в аккаунт владельца"],
    ["NotFound", "Ресторан не найден. Возможно, ссылка устарела."],
    ["Unavailable", "Не удалось изменить статус."],
  ])("maps the BFF error %s to a message", async (code, message) => {
    const user = userEvent.setup();
    mockServer.use(
      publicGraphql.mutation(
        "CloseRestaurant",
        () =>
          HttpResponse.json({
            data: null,
            errors: [bffError(code, "refused").toJSON()],
          }),
        { once: true },
      ),
    );
    renderManage();

    await user.click(
      await screen.findByRole("switch", {
        name: "Приём заказов",
        checked: true,
      }),
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(message);
  });

  // AC-757: a transport error is retried by hand only.
  it("offers a manual retry after a transport error", async () => {
    const user = userEvent.setup();
    mockServer.use(
      publicGraphql.mutation("CloseRestaurant", () => HttpResponse.error(), {
        once: true,
      }),
    );
    renderManage();

    const close = await screen.findByRole("switch", {
      name: "Приём заказов",
      checked: true,
    });
    await user.click(close);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Не удалось изменить статус.",
    );

    await user.click(close);
    expect(
      await screen.findByRole("switch", {
        name: "Приём заказов",
        checked: false,
      }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

describe("manage restaurant quality", () => {
  it("has no detectable accessibility violations", async () => {
    const { container } = renderManage(bakery);
    await screen.findByRole("switch", {
      name: "Приём заказов",
      checked: false,
    });

    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});
