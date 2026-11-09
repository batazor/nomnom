// @vitest-environment node
import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

const theme = readFileSync(new URL("./theme.css", import.meta.url), "utf8");
const button = readFileSync(
  new URL("../ui/button.tsx", import.meta.url),
  "utf8",
);

describe("semantic theme", () => {
  it("provides dark theme and reduced-motion overrides", () => {
    expect(theme).toContain(':root[data-theme="dark"]');
    expect(theme).toContain("prefers-color-scheme: dark");
    expect(theme).toContain("prefers-reduced-motion: reduce");
    expect(theme).toMatch(/--motion-fast:\s*0ms/);
  });

  it("tints browser chrome with the page background of each scheme", () => {
    const html = readFileSync(
      new URL("../../../index.html", import.meta.url),
      "utf8",
    );
    const background = (block: string) =>
      block.match(/--color-background:\s*(#[0-9a-f]{6})/i)?.[1];
    const themeColor = (scheme: string) =>
      html.match(
        new RegExp(
          `name="theme-color" media="\\(prefers-color-scheme: ${scheme}\\)" content="(#[0-9a-f]{6})"`,
          "i",
        ),
      )?.[1];

    const [light, dark] = theme.split(':root[data-theme="dark"]');
    expect(themeColor("light")).toBe(background(light));
    expect(themeColor("dark")).toBe(background(dark));
  });

  it("keeps component source free of hardcoded palette values", () => {
    expect(button).not.toMatch(/#[0-9a-f]{3,8}|rgb\(/i);
  });
});
