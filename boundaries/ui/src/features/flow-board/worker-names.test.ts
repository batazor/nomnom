import { describe, expect, it } from "vitest";

import { newWorkerName } from "./worker-names";

describe("newWorkerName", () => {
  it("makes up a first name with a last-name initial", () => {
    expect(newWorkerName("ru", [], () => 0)).toBe("Анна А.");
    expect(newWorkerName("en", [], () => 0)).toBe("Alex A.");
  });

  it("skips names already in the pool", () => {
    expect(newWorkerName("ru", ["Анна А."], () => 0)).toBe("Анна Б.");
  });
});
