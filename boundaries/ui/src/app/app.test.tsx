import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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
  it("opens the restaurant catalog on the mobile-first home route", async () => {
    Object.defineProperty(window, "innerWidth", {
      value: 390,
      configurable: true,
    });
    renderApp("/");

    expect(screen.getByRole("main")).toBeInTheDocument();
    expect(
      await screen.findByRole("heading", { level: 1, name: "Рестораны рядом" }),
    ).toBeInTheDocument();
    expect(document.querySelector(".app-shell")).toBeInTheDocument();
    expect(document.body.scrollWidth).toBeLessThanOrEqual(window.innerWidth);
  });

  it("leads back to the catalog from the brand", async () => {
    const user = userEvent.setup();
    renderApp("/orders/order-preparing");
    await screen.findByRole("heading", { level: 1, name: "Готовится" });

    await user.click(screen.getByRole("link", { name: "nomnom" }));
    expect(
      await screen.findByRole("heading", { level: 1, name: "Рестораны рядом" }),
    ).toBeInTheDocument();
  });

  it("has no detectable accessibility violations", async () => {
    const { container } = renderApp("/");
    await screen.findByRole("heading", { level: 1, name: "Рестораны рядом" });
    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});
