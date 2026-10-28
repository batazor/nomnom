import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { graphql } from "msw/graphql";
import {
  afterAll,
  afterEach,
  beforeAll,
  beforeEach,
  describe,
  expect,
  it,
} from "vitest";

import { AppProviders } from "../../app/providers";
import { mockStore } from "../../shared/graphql/mocks/handlers";
import { mockServer } from "../../shared/graphql/mocks/server";
import { OpeningHours } from "./opening-hours";

const config = {
  publicApiUrl: "https://public.example.test",
  locale: "ru" as const,
  enableMocks: true,
  environment: "test",
};

// Кухня №8 opens every day from 10:00 to 23:00.
const kitchen = "restaurant-kitchen-8";
const publicGraphql = graphql.link(/\/graphql$/);

beforeAll(() => mockServer.listen({ onUnhandledFrame: "error" }));
beforeEach(() => localStorage.setItem("nomnom.market.v1", "berlin"));
afterEach(() => {
  mockServer.resetHandlers();
  mockStore.reset();
  localStorage.clear();
});
afterAll(() => mockServer.close());

type Interval = { dayOfWeek: number; opens: string; closes: string };

function recordSetOpeningHours() {
  const requests: Interval[][] = [];
  mockServer.use(
    publicGraphql.mutation<never, { openingHours: Interval[] }>(
      "SetOpeningHours",
      ({ variables }) => {
        requests.push(variables.openingHours);
      },
    ),
  );
  return requests;
}

async function renderHours() {
  const view = render(
    <AppProviders config={config}>
      <main>
        <h1>Доска заказа</h1>
        <h2>Кухня</h2>
        <OpeningHours restaurantId={kitchen} />
      </main>
    </AppProviders>,
  );
  await screen.findByRole("region", { name: "Часы работы" });
  return view;
}

// The row of a weekday: its intervals and its add button.
function day(name: string) {
  const row = screen
    .getAllByRole("listitem")
    .find((item) => item.textContent?.startsWith(name));
  if (!row) throw new Error(`No row ${name}`);
  return row;
}

function setTimes(name: string, index: number, opens: string, closes: string) {
  const row = within(day(name));
  fireEvent.change(row.getAllByLabelText(`${name}, Открытие`)[index], {
    target: { value: opens },
  });
  fireEvent.change(row.getAllByLabelText(`${name}, Закрытие`)[index], {
    target: { value: closes },
  });
}

describe("opening hours", () => {
  // AC-951: an interval into the next day is saved with the whole schedule.
  it("saves the whole schedule with an interval into the next day", async () => {
    const user = userEvent.setup();
    const requests = recordSetOpeningHours();
    await renderHours();

    await user.click(
      within(day("Пятница")).getByRole("button", {
        name: "Убрать интервал Пятница 10:00–23:00",
      }),
    );
    await user.click(
      screen.getByRole("button", { name: "Добавить интервал: Пятница" }),
    );
    setTimes("Пятница", 0, "18:00", "02:00");
    expect(
      within(day("Пятница")).getByText("до утра следующего дня"),
    ).toBeVisible();
    await user.click(
      screen.getByRole("button", { name: "Сохранить расписание" }),
    );

    expect(await screen.findByText("Расписание сохранено.")).toBeVisible();
    expect(requests).toHaveLength(1);
    expect(requests[0]).toHaveLength(7);
    expect(requests[0]).toContainEqual({
      dayOfWeek: 5,
      opens: "18:00",
      closes: "02:00",
    });
    expect(requests[0]).not.toContainEqual({
      dayOfWeek: 5,
      opens: "10:00",
      closes: "23:00",
    });
    // The form starts over from the saved schedule.
    expect(
      await within(day("Пятница")).findByRole("button", {
        name: "Убрать интервал Пятница 18:00–02:00",
      }),
    ).toBeVisible();
  });

  // AC-950: the overlap restaurants rejects is shown at that interval.
  it("shows an overlap at the interval it names", async () => {
    const user = userEvent.setup();
    await renderHours();

    await user.click(
      screen.getByRole("button", { name: "Добавить интервал: Понедельник" }),
    );
    setTimes("Понедельник", 1, "12:00", "14:00");
    await user.click(
      screen.getByRole("button", { name: "Сохранить расписание" }),
    );

    const alert = await within(day("Понедельник")).findByRole("alert");
    expect(alert).toHaveTextContent("Пересекается с другим интервалом");
    const [rejected] = within(day("Понедельник"))
      .getAllByLabelText("Понедельник, Открытие")
      .filter((input) => input.getAttribute("aria-invalid") === "true");
    expect(rejected).toHaveValue("12:00");
  });

  it("passes axe", async () => {
    const { container } = await renderHours();
    const results = await axe.run(container);
    expect(results.violations).toEqual([]);
  });
});
