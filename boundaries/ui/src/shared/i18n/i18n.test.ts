import { describe, expect, it } from "vitest";

import { createFormatters, createTranslator } from "./i18n";

describe("i18n foundation", () => {
  it("reads localized messages and interpolates values", () => {
    expect(createTranslator("ru")("status.ready")).toBe(
      "Foundation готов к подключению продуктовых features",
    );
  });

  it("falls back to Russian messages for an unknown locale", () => {
    expect(createTranslator("id")("home.action")).toBe("Посмотреть основу");
  });

  it("formats numbers and dates with the configured locale", () => {
    const ru = createFormatters("ru");
    expect(ru.number.format(12500)).toMatch(/12.?500/);
    expect(ru.date.format(new Date("2026-10-24T00:00:00Z"))).toContain("2026");
  });
});
