import { describe, expect, it } from "vitest";

import { parseMarketId } from "./market";

describe("stored Market", () => {
  it("keeps only a market_id slug", () => {
    expect(parseMarketId("berlin")).toBe("berlin");
    expect(parseMarketId("new-york-2")).toBe("new-york-2");
    expect(parseMarketId(null)).toBeUndefined();
    expect(parseMarketId("")).toBeUndefined();
    expect(parseMarketId("Berlin")).toBeUndefined();
    expect(parseMarketId("b")).toBeUndefined();
    expect(parseMarketId("1berlin")).toBeUndefined();
  });
});
