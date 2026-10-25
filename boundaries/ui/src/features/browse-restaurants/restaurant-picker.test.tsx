import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
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
import { mockStore } from "../../shared/graphql/mocks/handlers";
import { mockServer } from "../../shared/graphql/mocks/server";
import { RestaurantPicker } from "./restaurant-picker";
import { restaurantsPageSize } from "./use-restaurant-pages";

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
});
afterAll(() => mockServer.close());

function renderPicker(onSelect = vi.fn()) {
  const { container } = render(
    <AppProviders config={config}>
      <main>
        <RestaurantPicker onSelect={onSelect} />
      </main>
    </AppProviders>,
  );
  return { onSelect, container };
}

function restaurantButtons() {
  return within(
    screen.getByRole("list", { name: "Рестораны рядом" }),
  ).getAllByRole("button");
}

describe("restaurant picker", () => {
  it("lists open restaurants and loads more on request", async () => {
    const user = userEvent.setup();
    renderPicker();

    expect(
      await screen.findByRole("button", { name: /Кухня №8/ }),
    ).toBeInTheDocument();
    expect(restaurantButtons()).toHaveLength(restaurantsPageSize);
    expect(screen.queryByText("Ночная пекарня")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Показать ещё" }));

    const added = await screen.findByRole("button", {
      name: /Кофейня на углу/,
    });
    expect(restaurantButtons()).toHaveLength(restaurantsPageSize + 1);
    expect(added).toHaveFocus();
    expect(
      screen.queryByRole("button", { name: "Показать ещё" }),
    ).not.toBeInTheDocument();
  });

  it("hands the chosen restaurant to the board", async () => {
    const user = userEvent.setup();
    const { onSelect } = renderPicker();

    await screen.findByRole("button", { name: /Кухня №8/ });
    await user.click(screen.getByRole("button", { name: /Пельменная/ }));
    expect(onSelect).toHaveBeenCalledWith("restaurant-north-dumplings");
  });

  it("keeps loaded restaurants when the next page fails", async () => {
    const user = userEvent.setup();
    renderPicker();
    await screen.findByRole("button", { name: /Кухня №8/ });

    mockServer.use(
      publicGraphql.query("BrowseRestaurants", () => HttpResponse.error(), {
        once: true,
      }),
    );
    await user.click(screen.getByRole("button", { name: "Показать ещё" }));

    expect(
      await screen.findByText("Не удалось загрузить следующие рестораны."),
    ).toBeVisible();
    expect(restaurantButtons()).toHaveLength(restaurantsPageSize);

    await user.click(screen.getByRole("button", { name: "Повторить" }));
    expect(
      await screen.findByRole("button", { name: /Кофейня на углу/ }),
    ).toBeInTheDocument();
  });

  it("offers recovery without technical details when the catalog fails", async () => {
    const user = userEvent.setup();
    mockServer.use(
      publicGraphql.query("BrowseRestaurants", () =>
        HttpResponse.json({
          data: null,
          errors: [{ message: "rpc error: code = Unavailable" }],
        }),
      ),
    );
    renderPicker();

    expect(
      await screen.findByText("Не удалось загрузить рестораны"),
    ).toBeInTheDocument();
    expect(screen.queryByText(/rpc|Unavailable/)).not.toBeInTheDocument();

    mockServer.resetHandlers();
    await user.click(screen.getByRole("button", { name: "Повторить" }));
    expect(
      await screen.findByRole("button", { name: /Кухня №8/ }),
    ).toBeInTheDocument();
  });

  it("says so when no restaurant is open", async () => {
    mockServer.use(
      publicGraphql.query("BrowseRestaurants", () =>
        HttpResponse.json({
          data: {
            restaurants: {
              edges: [],
              pageInfo: { hasNextPage: false, endCursor: null },
            },
          },
        }),
      ),
    );
    renderPicker();

    expect(await screen.findByText("Сейчас всё закрыто")).toBeInTheDocument();
  });

  it("fits a 390px viewport and passes axe", async () => {
    Object.defineProperty(window, "innerWidth", {
      value: 390,
      configurable: true,
    });
    const { container } = renderPicker();
    await screen.findByRole("button", { name: /Кухня №8/ });

    expect(document.body.scrollWidth).toBeLessThanOrEqual(window.innerWidth);
    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});
