import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { HttpResponse } from "msw";
import { graphql } from "msw/graphql";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";

import { AppProviders } from "../../app/providers";
import {
  mockStore,
  simulatorDeniedHandlers,
} from "../../shared/graphql/mocks/handlers";
import { mockServer } from "../../shared/graphql/mocks/server";
import { bffError } from "../../shared/graphql/mocks/store";
import { SimulatorToggle } from "./simulator-toggle";

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

function renderToggle() {
  return render(
    <AppProviders config={config}>
      <header>
        <SimulatorToggle />
      </header>
    </AppProviders>,
  );
}

function denied(code: string) {
  return publicGraphql.query("DemoSimulator", () =>
    HttpResponse.json({
      data: null,
      errors: [bffError(code, "refused").toJSON()],
    }),
  );
}

describe("demo simulator", () => {
  // AC-766: the operator switches the Simulator on and sees the answer.
  it("switches the simulator on after the answer", async () => {
    const user = userEvent.setup();
    let sent: unknown;
    mockServer.use(
      publicGraphql.mutation("SetSimulatorEnabled", ({ variables }) => {
        sent = variables.enabled;
        // Falls through to the schema-backed handler.
      }),
    );
    renderToggle();

    await user.click(
      await screen.findByRole("switch", { name: "Эмулятор", checked: false }),
    );

    expect(
      await screen.findByRole("switch", { name: "Эмулятор", checked: true }),
    ).toBeEnabled();
    expect(sent).toBe(true);
    expect(mockStore.rootValue.simulator()).toEqual({ enabled: true });
  });

  it("stays pending and unchanged until the mutation answers", async () => {
    const user = userEvent.setup();
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    let sent = 0;
    mockServer.use(
      publicGraphql.mutation("SetSimulatorEnabled", async () => {
        sent += 1;
        await gate;
      }),
    );
    renderToggle();

    const toggle = await screen.findByRole("switch", {
      name: "Эмулятор",
      checked: false,
    });
    await user.click(toggle);

    expect(
      screen.getByRole("switch", { name: "Эмулятор", checked: false }),
    ).toBeDisabled();
    expect(document.querySelector(".demo-simulator")).toHaveAttribute(
      "aria-busy",
      "true",
    );
    await user.click(toggle);
    expect(sent).toBe(1);

    release();
    expect(
      await screen.findByRole("switch", { name: "Эмулятор", checked: true }),
    ).toBeEnabled();
  });

  it("keeps the state and tells a failed switch", async () => {
    const user = userEvent.setup();
    mockServer.use(
      publicGraphql.mutation(
        "SetSimulatorEnabled",
        () =>
          HttpResponse.json({
            data: null,
            errors: [bffError("Unavailable", "refused").toJSON()],
          }),
        { once: true },
      ),
    );
    renderToggle();

    await user.click(
      await screen.findByRole("switch", { name: "Эмулятор", checked: false }),
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Не удалось переключить эмулятор.",
    );
    expect(
      screen.getByRole("switch", { name: "Эмулятор", checked: false }),
    ).toBeEnabled();
  });

  // AC-767: a customer gets PermissionDenied and sees no switch.
  it("shows no switch when the simulator is denied", async () => {
    let answered = false;
    mockServer.use(
      publicGraphql.query("DemoSimulator", () => {
        answered = true;
      }),
      ...simulatorDeniedHandlers,
    );
    renderToggle();

    await expect.poll(() => answered).toBe(true);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
  });

  it.each(["Unauthenticated", "Unavailable"])(
    "shows no switch when simulator answers %s",
    async (code) => {
      let answered = false;
      mockServer.use(
        publicGraphql.query("DemoSimulator", () => {
          answered = true;
        }),
        denied(code),
      );
      renderToggle();

      await expect.poll(() => answered).toBe(true);
      await new Promise((resolve) => setTimeout(resolve, 0));
      expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    },
  );
});

describe("demo simulator quality", () => {
  it("has no detectable accessibility violations", async () => {
    const { container } = renderToggle();
    await screen.findByRole("switch", { name: "Эмулятор" });

    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});
