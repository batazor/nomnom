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

  it("keeps component source free of hardcoded palette values", () => {
    expect(button).not.toMatch(/#[0-9a-f]{3,8}|rgb\(/i);
  });
});
