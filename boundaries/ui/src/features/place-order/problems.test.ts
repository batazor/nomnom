import { describe, expect, it } from "vitest";

import { splitProblems } from "./problems";

describe("place order problems", () => {
  it("puts problems with items next to those items", () => {
    const { orderProblems, itemProblems } = splitProblems([
      { code: "ORDER_ITEM_UNAVAILABLE", menuItemIds: ["menu-a", "menu-b"] },
    ]);
    expect(orderProblems).toEqual([]);
    expect([...itemProblems]).toEqual([
      ["menu-a", "checkout.itemProblem.unavailable"],
      ["menu-b", "checkout.itemProblem.unavailable"],
    ]);
  });

  it("shows problems without items once for the whole order", () => {
    const { orderProblems, itemProblems } = splitProblems([
      { code: "RESTAURANT_NOT_ACCEPTING_ORDERS", menuItemIds: [] },
      { code: "ORDER_TOTAL_BELOW_MINIMUM", menuItemIds: [] },
      { code: "ORDER_TOTAL_BELOW_MINIMUM", menuItemIds: [] },
    ]);
    expect(orderProblems).toEqual([
      "checkout.problem.RESTAURANT_NOT_ACCEPTING_ORDERS",
      "checkout.problem.ORDER_TOTAL_BELOW_MINIMUM",
    ]);
    expect(itemProblems.size).toBe(0);
  });
});
