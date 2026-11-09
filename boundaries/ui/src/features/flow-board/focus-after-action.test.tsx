import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useRef, useState } from "react";
import { describe, expect, it } from "vitest";

import { useFocusAfterAction } from "./focus-after-action";

// A board column whose cards leave when their button is pressed.
function Column({ initial }: { initial: readonly string[] }) {
  const [keys, setKeys] = useState(initial);
  const list = useRef<HTMLUListElement>(null);
  const focus = useFocusAfterAction(list);
  return (
    <section className="board-column">
      <header className="board-column-header">
        <h2 tabIndex={-1}>Кухня</h2>
      </header>
      {keys.length > 0 ? (
        <ul ref={list}>
          {keys.map((key) => (
            <li key={key}>
              <article data-focus-key={key}>
                <button className="board-card-head" type="button">
                  {`Заказ ${key}`}
                </button>
                <button
                  onClick={() => {
                    focus.mark(key);
                    setKeys((current) => current.filter((k) => k !== key));
                  }}
                  type="button"
                >
                  {`Отклонить ${key}`}
                </button>
              </article>
            </li>
          ))}
        </ul>
      ) : (
        <p>Заказов нет</p>
      )}
    </section>
  );
}

describe("focus after an action on a board card", () => {
  // AC-1104
  it("goes to the next card, then to the column heading once it is empty", async () => {
    const user = userEvent.setup();
    render(<Column initial={["a", "b"]} />);

    screen.getByRole("button", { name: "Отклонить a" }).focus();
    await user.keyboard("{Enter}");
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Заказ b" })).toHaveFocus(),
    );

    screen.getByRole("button", { name: "Отклонить b" }).focus();
    await user.keyboard("{Enter}");
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "Кухня" })).toHaveFocus(),
    );
  });
});
