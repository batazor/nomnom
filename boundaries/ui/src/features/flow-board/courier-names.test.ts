import { describe, expect, it } from "vitest";

import { newCourierName } from "./courier-names";

describe("newCourierName", () => {
  it("makes up a first name with a last-name initial", () => {
    expect(newCourierName("ru", [], () => 0)).toBe("Анна А.");
    expect(newCourierName("en", [], () => 0)).toBe("Alex A.");
  });

  it("skips names already in the pool", () => {
    expect(newCourierName("ru", ["Анна А."], () => 0)).toBe("Анна Б.");
  });
});
