import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { HttpResponse } from "msw";
import { graphql } from "msw/graphql";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";

import { AppProviders } from "../../app/providers";
import { AppRouter } from "../../app/router";
import { mockStore } from "../../shared/graphql/mocks/handlers";
import { mockServer } from "../../shared/graphql/mocks/server";
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

function renderCatalog() {
  return render(
    <AppProviders config={config}>
      <AppRouter initialEntries={["/"]} />
    </AppProviders>,
  );
}

function restaurantLinks() {
  return within(screen.getByRole("list")).getAllByRole("link");
}

describe("browse restaurants", () => {
  it("lists open restaurants and loads more on request", async () => {
    const user = userEvent.setup();
    renderCatalog();

    expect(
      await screen.findByRole("heading", { level: 1, name: "Рестораны рядом" }),
    ).toBeInTheDocument();
    expect(restaurantLinks()).toHaveLength(restaurantsPageSize);
    expect(screen.getByRole("link", { name: /Кухня №8/ })).toHaveAttribute(
      "href",
      "/restaurants/restaurant-kitchen-8",
    );
    expect(screen.queryByText("Ночная пекарня")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Показать ещё" }));

    const added = await screen.findByRole("link", { name: /Кофейня на углу/ });
    expect(restaurantLinks()).toHaveLength(restaurantsPageSize + 1);
    expect(added).toHaveFocus();
    expect(
      screen.queryByRole("button", { name: "Показать ещё" }),
    ).not.toBeInTheDocument();
  });

  it("opens the menu of a chosen restaurant", async () => {
    const user = userEvent.setup();
    renderCatalog();

    await user.click(await screen.findByRole("link", { name: /Кухня №8/ }));
    expect(
      await screen.findByRole("heading", { level: 1, name: "Кухня №8" }),
    ).toBeInTheDocument();
  });

  it("keeps loaded restaurants when the next page fails", async () => {
    const user = userEvent.setup();
    renderCatalog();
    await screen.findByRole("heading", { level: 1, name: "Рестораны рядом" });

    mockServer.use(
      publicGraphql.query("BrowseRestaurants", () => HttpResponse.error(), {
        once: true,
      }),
    );
    await user.click(screen.getByRole("button", { name: "Показать ещё" }));

    expect(
      await screen.findByText("Не удалось загрузить следующие рестораны."),
    ).toBeVisible();
    expect(restaurantLinks()).toHaveLength(restaurantsPageSize);

    await user.click(screen.getByRole("button", { name: "Повторить" }));
    expect(
      await screen.findByRole("link", { name: /Кофейня на углу/ }),
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
    renderCatalog();

    expect(
      await screen.findByRole("heading", {
        name: "Не удалось загрузить рестораны",
      }),
    ).toBeInTheDocument();
    expect(screen.queryByText(/rpc|Unavailable/)).not.toBeInTheDocument();

    mockServer.resetHandlers();
    await user.click(screen.getByRole("button", { name: "Повторить" }));
    expect(
      await screen.findByRole("heading", { level: 1, name: "Рестораны рядом" }),
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
    renderCatalog();

    expect(
      await screen.findByRole("heading", { name: "Сейчас всё закрыто" }),
    ).toBeInTheDocument();
  });

  it("fits a 390px viewport and passes axe", async () => {
    Object.defineProperty(window, "innerWidth", {
      value: 390,
      configurable: true,
    });
    const { container } = renderCatalog();
    await screen.findByRole("heading", { level: 1, name: "Рестораны рядом" });

    expect(document.body.scrollWidth).toBeLessThanOrEqual(window.innerWidth);
    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});
