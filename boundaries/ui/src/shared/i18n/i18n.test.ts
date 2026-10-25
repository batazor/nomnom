import { describe, expect, it } from "vitest";

import { createFormatters, createTranslator } from "./i18n";

describe("i18n foundation", () => {
  it("reads localized messages and interpolates values", () => {
    expect(createTranslator("ru")("menu.inCart", { count: 2 })).toBe(
      "В корзине: 2",
    );
  });

  it("falls back to Russian messages for an unknown locale", () => {
    expect(createTranslator("id")("menu.title")).toBe("Меню");
  });

  it("formats numbers and dates with the configured locale", () => {
    const ru = createFormatters("ru");
    expect(ru.number.format(12500)).toMatch(/12.?500/);
    expect(ru.date.format(new Date("2026-10-24T00:00:00Z"))).toContain("2026");
  });
});
