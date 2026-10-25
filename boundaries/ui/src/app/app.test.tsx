import axe from "axe-core";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { AppProviders } from "./providers";
import { AppRouter } from "./router";

const config = {
  publicGraphqlUrl: "https://public.example.test/graphql",
  locale: "ru" as const,
  enableMocks: true,
};

describe("customer application shell", () => {
  it("renders and navigates the localized mobile-first route", async () => {
    const user = userEvent.setup();
    Object.defineProperty(window, "innerWidth", {
      value: 390,
      configurable: true,
    });
    render(
      <AppProviders config={config}>
        <AppRouter initialEntries={["/"]} />
      </AppProviders>,
    );

    expect(screen.getByRole("main")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Один путь от меню до двери" }),
    ).toBeInTheDocument();
    expect(document.querySelectorAll('[data-ui-primary="true"]')).toHaveLength(
      1,
    );
    expect(document.querySelector(".app-shell")).toBeInTheDocument();
    expect(document.body.scrollWidth).toBeLessThanOrEqual(window.innerWidth);
    await user.click(screen.getByRole("link", { name: "BiteRoute" }));
    expect(screen.getByRole("main")).toBeInTheDocument();
  });

  it("keeps the placeholder action local without a backend call", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const user = userEvent.setup();
    render(
      <AppProviders config={config}>
        <AppRouter initialEntries={["/"]} />
      </AppProviders>,
    );

    await user.click(screen.getByRole("button", { name: "Проверить заказ" }));
    expect(
      screen.getByText("Fixture-заказ готов к просмотру"),
    ).toBeInTheDocument();
    expect(fetchSpy).not.toHaveBeenCalled();
    fetchSpy.mockRestore();
  });

  it("has no detectable accessibility violations", async () => {
    const { container } = render(
      <AppProviders config={config}>
        <AppRouter initialEntries={["/"]} />
      </AppProviders>,
    );
    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});
