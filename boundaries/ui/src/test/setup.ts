import "@testing-library/jest-dom/vitest";

import { cleanup } from "@testing-library/react";
import { MotionGlobalConfig } from "motion/react";
import { afterEach } from "vitest";

if (typeof HTMLCanvasElement !== "undefined") {
  Object.defineProperty(HTMLCanvasElement.prototype, "getContext", {
    configurable: true,
    value: () => null,
  });
}

// Animations jump to their end: content that fades in after a card grows is
// there at once, and tests see the settled screen.
MotionGlobalConfig.skipAnimations = true;

afterEach(() => cleanup());
