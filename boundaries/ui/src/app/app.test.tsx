import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";

import { mockStore } from "../shared/graphql/mocks/handlers";
import { mockServer } from "../shared/graphql/mocks/server";
import { AppProviders } from "./providers";
import { AppRouter } from "./router";

const config = {
  publicApiUrl: "https://public.example.test",
  locale: "ru" as const,
  enableMocks: true,
  environment: "test",
};

beforeAll(() => mockServer.listen({ onUnhandledFrame: "error" }));
afterEach(() => {
  mockServer.resetHandlers();
  mockStore.reset();
  localStorage.clear();
});
afterAll(() => mockServer.close());

function renderApp(path: string) {
  return render(
    <AppProviders config={config}>
      <AppRouter initialEntries={[path]} />
    </AppProviders>,
  );
}

const boardHeading = { level: 1, name: "Доска заказа" } as const;

async function chooseBerlin() {
  await userEvent.click(
    await screen.findByRole("button", { name: "Выбрать: Берлин" }),
  );
}

describe("customer application shell", () => {
  it("asks for a Market before the board, even with markets loaded", async () => {
    renderApp("/");

    expect(screen.getByRole("main")).toBeInTheDocument();
    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Где вы заказываете?",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Выбрать: Гамбург" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("heading", boardHeading)).not.toBeInTheDocument();
  });

  it("opens the order board in the chosen Market and remembers it", async () => {
    renderApp("/");
    await chooseBerlin();

    expect(
      await screen.findByRole("heading", boardHeading),
    ).toBeInTheDocument();
    expect(localStorage.getItem("nomnom.market.v1")).toBe("berlin");
    expect(screen.getByRole("combobox", { name: "Город" })).toHaveValue(
      "berlin",
    );
  });

  it("opens the board at once in a remembered Market", async () => {
    localStorage.setItem("nomnom.market.v1", "hamburg");
    renderApp("/");
    expect(
      await screen.findByRole("heading", boardHeading),
    ).toBeInTheDocument();
  });

  it("asks again when the remembered Market is no longer listed", async () => {
    localStorage.setItem("nomnom.market.v1", "paris");
    renderApp("/");
    expect(
      await screen.findByText(
        "Выбранный раньше город больше не обслуживается. Выберите другой.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole("heading", boardHeading)).not.toBeInTheDocument();
  });

  it("switches the Market from the header", async () => {
    localStorage.setItem("nomnom.market.v1", "berlin");
    renderApp("/");
    await screen.findByRole("heading", boardHeading);
    const header = document.querySelector(".app-topbar") as HTMLElement;
    await userEvent.selectOptions(
      within(header).getByRole("combobox", { name: "Город" }),
      "hamburg",
    );
    expect(localStorage.getItem("nomnom.market.v1")).toBe("hamburg");
    expect(
      await screen.findByRole("heading", boardHeading),
    ).toBeInTheDocument();
  });

  it("addresses every operation to the chosen Market by path", async () => {
    const requests: { path: string; market: string | null }[] = [];
    mockServer.events.on("request:start", ({ request }) => {
      requests.push({
        path: new URL(request.url).pathname,
        market: request.headers.get("x-nomnom-market"),
      });
    });
    renderApp("/");
    await chooseBerlin();
    await screen.findByRole("heading", boardHeading);

    // The list of Markets comes from the public gateway, not from a Cell.
    expect(requests[0]?.path).toBe("/markets");
    const afterChoice = requests.slice(1);
    expect(afterChoice.length).toBeGreaterThan(0);
    expect(
      afterChoice.every((request) => request.path === "/m/berlin/graphql"),
    ).toBe(true);
    // The gateway sets the Market header from the route, never the browser.
    expect(requests.every((request) => request.market === null)).toBe(true);
    mockServer.events.removeAllListeners();
  });

  it("leads any other address back to the board", async () => {
    localStorage.setItem("nomnom.market.v1", "berlin");
    renderApp("/orders/order-preparing");
    expect(
      await screen.findByRole("heading", boardHeading),
    ).toBeInTheDocument();
  });

  it("has no detectable accessibility violations", async () => {
    localStorage.setItem("nomnom.market.v1", "berlin");
    const { container } = renderApp("/");
    await screen.findByRole("heading", boardHeading);
    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });

  it("has no detectable accessibility violations on the Market picker", async () => {
    const { container } = renderApp("/");
    await screen.findByRole("button", { name: "Выбрать: Берлин" });
    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});
