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
import { bffError } from "../../shared/graphql/mocks/store";

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

function renderMyRestaurants() {
  return render(
    <AppProviders config={config}>
      <AppRouter initialEntries={["/manage/restaurants"]} />
    </AppProviders>,
  );
}

describe("my restaurants", () => {
  it("lists open and closed restaurants of the owner and links to their management", async () => {
    const user = userEvent.setup();
    renderMyRestaurants();

    expect(
      await screen.findByRole("heading", { level: 1, name: "Мои рестораны" }),
    ).toBeInTheDocument();
    const items = screen.getAllByRole("listitem");
    expect(items.map((item) => item.textContent)).toEqual([
      expect.stringContaining("Кухня №8"),
      expect.stringContaining("Ночная пекарня"),
    ]);
    expect(within(items[0]).getByText("Открыт")).toBeVisible();
    expect(within(items[1]).getByText("Закрыт")).toBeVisible();

    await user.click(screen.getByRole("link", { name: /Ночная пекарня/ }));

    expect(
      await screen.findByRole("heading", { level: 1, name: "Ночная пекарня" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Мои рестораны/ })).toHaveAttribute(
      "href",
      "/manage/restaurants",
    );
  });

  it("tells an owner without restaurants so", async () => {
    mockServer.use(
      publicGraphql.query("MyRestaurants", () =>
        HttpResponse.json({
          data: {
            myRestaurants: { __typename: "RestaurantConnection", edges: [] },
          },
        }),
      ),
    );
    renderMyRestaurants();

    expect(
      await screen.findByText("У вас пока нет ресторанов."),
    ).toBeInTheDocument();
  });

  it("asks to sign in when the BFF answers Unauthenticated and retries", async () => {
    const user = userEvent.setup();
    mockServer.use(
      publicGraphql.query(
        "MyRestaurants",
        () =>
          HttpResponse.json({
            data: null,
            errors: [bffError("Unauthenticated", "no token").toJSON()],
          }),
        { once: true },
      ),
    );
    renderMyRestaurants();

    expect(
      await screen.findByRole("heading", {
        name: "Не удалось загрузить рестораны",
      }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Повторить" }));

    expect(
      await screen.findByRole("heading", { level: 1, name: "Мои рестораны" }),
    ).toBeInTheDocument();
  });

  it("passes axe", async () => {
    const { container } = renderMyRestaurants();
    await screen.findByRole("heading", { level: 1, name: "Мои рестораны" });

    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});
