import { describe, expect, it } from "vitest";

import { estimateSubtotal } from "./money";

const rub = (amount: string) => ({ amount, currency: "RUB" });

describe("estimated subtotal", () => {
  it("multiplies and sums decimal strings exactly", () => {
    expect(
      estimateSubtotal([
        { price: rub("0.10"), quantity: 3 },
        { price: rub("0.20"), quantity: 1 },
      ]),
    ).toEqual(rub("0.50"));
    expect(
      estimateSubtotal([
        { price: rub("690.00"), quantity: 2 },
        { price: rub("420.00"), quantity: 1 },
      ]),
    ).toEqual(rub("1800.00"));
  });

  it("aligns amounts with different fraction digits", () => {
    expect(
      estimateSubtotal([
        { price: rub("12.5"), quantity: 1 },
        { price: rub("0.255"), quantity: 2 },
        { price: rub("3"), quantity: 1 },
      ]),
    ).toEqual(rub("16.010"));
  });

  it("keeps large amounts exact", () => {
    expect(
      estimateSubtotal([{ price: rub("9007199254740993.01"), quantity: 3 }]),
    ).toEqual(rub("27021597764222979.03"));
  });

  it("gives no estimate for an empty cart or mixed currencies", () => {
    expect(estimateSubtotal([])).toBeNull();
    expect(
      estimateSubtotal([
        { price: rub("1.00"), quantity: 1 },
        { price: { amount: "1.00", currency: "EUR" }, quantity: 1 },
      ]),
    ).toBeNull();
  });

  it("rejects an amount that is not a decimal string", () => {
    expect(() =>
      estimateSubtotal([{ price: rub("1e3"), quantity: 1 }]),
    ).toThrow("Not a decimal amount");
  });
});
