import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { Button } from "./button";

describe("Button", () => {
  // AC-1102
  it("keeps focus and ignores presses while its action runs", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    const { rerender } = render(<Button onClick={onClick}>Принять</Button>);
    const button = screen.getByRole("button", { name: "Принять" });

    await user.click(button);
    rerender(
      <Button isPending onClick={onClick}>
        Принять
      </Button>,
    );

    expect(button).toHaveFocus();
    expect(button).toHaveAttribute("aria-disabled", "true");
    expect(button).toHaveAttribute("aria-busy", "true");
    await user.keyboard("{Enter}");
    await user.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("does not submit its form while pending", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn((event: SubmitEvent) => event.preventDefault());
    render(
      <form
        onSubmit={(event) => onSubmit(event.nativeEvent as SubmitEvent)}
      >
        <Button isPending type="submit">
          Оформить заказ
        </Button>
      </form>,
    );

    await user.click(screen.getByRole("button", { name: "Оформить заказ" }));

    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("stays out of reach while disabled before anyone presses it", () => {
    render(<Button disabled>Убрать</Button>);

    expect(screen.getByRole("button", { name: "Убрать" })).toBeDisabled();
  });
});
