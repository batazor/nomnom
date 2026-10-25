import { describe, expect, it } from "vitest";

import {
  addLine,
  maxQuantity,
  parseCart,
  removeLine,
  setLineQuantity,
} from "./cart";

describe("cart lines", () => {
  it("adds a new item and increments an existing one", () => {
    const once = addLine([], "menu-bao");
    expect(once).toEqual([{ menuItemId: "menu-bao", quantity: 1 }]);
    expect(addLine(once, "menu-bao")).toEqual([
      { menuItemId: "menu-bao", quantity: 2 },
    ]);
  });

  it("keeps quantities between one and the input limit", () => {
    const lines = [{ menuItemId: "menu-bao", quantity: 3 }];
    expect(setLineQuantity(lines, "menu-bao", 0)[0].quantity).toBe(1);
    expect(setLineQuantity(lines, "menu-bao", 500)[0].quantity).toBe(
      maxQuantity,
    );
    expect(removeLine(lines, "menu-bao")).toEqual([]);
  });

  it("reads only well-formed stored lines", () => {
    expect(parseCart(null)).toEqual([]);
    expect(parseCart("not json")).toEqual([]);
    expect(parseCart('{"lines":"oops"}')).toEqual([]);
    expect(
      parseCart(
        JSON.stringify({
          lines: [
            { menuItemId: "menu-bao", quantity: 2 },
            { menuItemId: "menu-tom-yum", quantity: 0 },
            { menuItemId: "", quantity: 1 },
            { menuItemId: "menu-mango", quantity: 1.5 },
            null,
          ],
        }),
      ),
    ).toEqual([{ menuItemId: "menu-bao", quantity: 2 }]);
  });
});
