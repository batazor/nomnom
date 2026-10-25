import { render, screen } from "@testing-library/react";
import axe from "axe-core";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";

import { mockStore } from "../shared/graphql/mocks/handlers";
import { mockServer } from "../shared/graphql/mocks/server";
import { AppProviders } from "./providers";
import { AppRouter } from "./router";

const config = {
  publicGraphqlUrl: "https://public.example.test/graphql",
  locale: "ru" as const,
  enableMocks: true,
  environment: "test",
};

beforeAll(() => mockServer.listen({ onUnhandledFrame: "error" }));
afterEach(() => {
  mockServer.resetHandlers();
  mockStore.reset();
});
afterAll(() => mockServer.close());

function renderApp(path: string) {
  return render(
    <AppProviders config={config}>
      <AppRouter initialEntries={[path]} />
    </AppProviders>,
  );
}

describe("customer application shell", () => {
  it("opens the order board on the home route", async () => {
    renderApp("/");

    expect(screen.getByRole("main")).toBeInTheDocument();
    expect(
      await screen.findByRole("heading", { level: 1, name: "Доска заказа" }),
    ).toBeInTheDocument();
  });

  it("leads any other address back to the board", async () => {
    renderApp("/orders/order-preparing");
    expect(
      await screen.findByRole("heading", { level: 1, name: "Доска заказа" }),
    ).toBeInTheDocument();
  });

  it("has no detectable accessibility violations", async () => {
    const { container } = renderApp("/");
    await screen.findByRole("heading", { level: 1, name: "Доска заказа" });
    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});
