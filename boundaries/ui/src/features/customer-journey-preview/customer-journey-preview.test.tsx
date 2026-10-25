import axe from "axe-core";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../../shared/i18n/i18n";
import { CustomerJourneyPreview } from "./customer-journey-preview";

function renderPreview() {
  return render(
    <I18nProvider locale="ru">
      <CustomerJourneyPreview />
    </I18nProvider>,
  );
}

describe("customer journey preview", () => {
  it("updates menu quantities and the linked checkout total by keyboard", async () => {
    const user = userEvent.setup();
    renderPreview();

    expect(screen.getByTestId("journey-total")).toHaveTextContent(/1.?798/);
    await user.tab();
    expect(
      screen.getByRole("button", {
        name: "Уменьшить количество: Том ям с креветками",
      }),
    ).toHaveFocus();
    const addMango = screen.getByRole("button", {
      name: "Увеличить количество: Манго и клейкий рис",
    });
    addMango.focus();
    await user.keyboard("{Enter}");

    expect(screen.getByTestId("journey-total")).toHaveTextContent(/2.?238/);
  });

  it("shows checkout details with one primary action and no business request", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const user = userEvent.setup();
    renderPreview();

    expect(
      screen.getByText("ул. Лесная, 12 · квартира 47"),
    ).toBeInTheDocument();
    expect(screen.getByText("Карта •••• 4242")).toBeInTheDocument();
    expect(document.querySelectorAll('[data-ui-primary="true"]')).toHaveLength(
      1,
    );
    await user.click(screen.getByRole("button", { name: "Проверить заказ" }));
    expect(
      screen.getByText("Fixture-заказ готов к просмотру"),
    ).toBeInTheDocument();
    expect(fetchSpy).not.toHaveBeenCalled();
    fetchSpy.mockRestore();
  });

  it("advances customer-facing delivery progress in a live region", async () => {
    const user = userEvent.setup();
    renderPreview();

    expect(screen.getByText("Курьер назначен")).toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Показать следующий этап" }),
    );
    expect(screen.getByText("Заказ забран")).toBeInTheDocument();
    expect(screen.getByText("Курьер направляется к вам")).toBeInTheDocument();
  });

  it("has no detectable accessibility violations", async () => {
    const { container } = renderPreview();
    const result = await axe.run(container, {
      rules: { "color-contrast": { enabled: false } },
    });
    expect(result.violations).toEqual([]);
  });
});
