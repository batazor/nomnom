// @vitest-environment node
import { describe, expect, it } from "vitest";

import { customerJourneyFixture, initialJourneyState } from "./fixtures";
import { deriveSubtotalMinor, journeyReducer } from "./model";

describe("customer journey fixture model", () => {
  it("uses stable fixture identifiers and integer minor-unit prices", () => {
    expect(customerJourneyFixture.restaurant.id).toBe(
      "restaurant-fixture-kitchen-8",
    );
    expect(customerJourneyFixture.courier.id).toBe("courier-fixture-alexey");
    expect(new Set(customerJourneyFixture.menu.map(({ id }) => id)).size).toBe(
      customerJourneyFixture.menu.length,
    );
    expect(
      customerJourneyFixture.menu.every(({ priceMinor }) =>
        Number.isSafeInteger(priceMinor),
      ),
    ).toBe(true);
  });

  it("keeps the initial selection consistent with menu fixtures", () => {
    const menuIds = new Set(customerJourneyFixture.menu.map(({ id }) => id));
    expect(
      Object.keys(initialJourneyState.quantities).every((id) =>
        menuIds.has(id as never),
      ),
    ).toBe(true);
    expect(
      deriveSubtotalMinor(
        customerJourneyFixture.menu,
        initialJourneyState.quantities,
      ),
    ).toBe(157000);
  });

  it("updates quantities without mutating the fixture state", () => {
    const incremented = journeyReducer(initialJourneyState, {
      type: "increment",
      itemId: "mango-rice",
    });
    const decremented = journeyReducer(incremented, {
      type: "decrement",
      itemId: "tom-yum",
    });

    expect(initialJourneyState.quantities["mango-rice"]).toBe(0);
    expect(incremented.quantities["mango-rice"]).toBe(1);
    expect(decremented.quantities["tom-yum"]).toBe(0);
    expect(
      deriveSubtotalMinor(customerJourneyFixture.menu, decremented.quantities),
    ).toBe(122000);
  });

  it("never decrements below zero and switches presentation stages", () => {
    const unchanged = journeyReducer(initialJourneyState, {
      type: "decrement",
      itemId: "mango-rice",
    });
    const advanced = journeyReducer(unchanged, {
      type: "set-delivery-stage",
      stageId: "picked-up",
    });

    expect(unchanged.quantities["mango-rice"]).toBe(0);
    expect(advanced.deliveryStage).toBe("picked-up");
  });
});
