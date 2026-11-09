import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { createTranslator, I18nProvider } from "./i18n";

describe("i18n foundation", () => {
  it("reads localized messages and interpolates values", () => {
    expect(createTranslator("ru")("menu.inCart", { count: 2 })).toBe(
      "В корзине: 2",
    );
  });

  it("rejects an unknown locale", () => {
    expect(() => createTranslator("id")).toThrow("Unsupported locale: id");
  });

  // AC-1107
  it("sets the document language from the configured locale", () => {
    render(<I18nProvider locale="en">{null}</I18nProvider>);

    expect(document.documentElement).toHaveAttribute("lang", "en");
  });
});
