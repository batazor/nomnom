import { describe, expect, it } from "vitest";

import { createTranslator } from "./i18n";

describe("i18n foundation", () => {
  it("reads localized messages and interpolates values", () => {
    expect(createTranslator("ru")("menu.inCart", { count: 2 })).toBe(
      "В корзине: 2",
    );
  });

  it("falls back to Russian messages for an unknown locale", () => {
    expect(createTranslator("id")("menu.title")).toBe("Меню");
  });
});
