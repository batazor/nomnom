import assert from "node:assert/strict";
import test from "node:test";

import {
  InvalidMenuError,
  InvalidMenuReason,
  Menu,
} from "../slices/menu/domains/menu/menu.ts";
import {
  InvalidMoneyError,
  InvalidMoneyReason,
  Money,
} from "../slices/menu/domains/menu/vo/money.ts";

const at = new Date(Date.UTC(2026, 9, 25, 9));

test("Money keeps exact decimal amounts and their scale", () => {
  const price = Money.of("0.10", "EUR");

  assert.equal(price.amount, "0.10");
  assert.equal(price.currency, "EUR");
  assert.equal(
    Money.of("12345678901234567890.123456789", "USD").amount,
    "12345678901234567890.123456789",
  );
  assert.equal(Money.of("007.5", "USD").amount, "7.5");
  assert.ok(price.equals(Money.of("0.1", "EUR")));
  assert.ok(!price.equals(Money.of("0.10", "USD")));
  assert.deepEqual(Money.of("0", "EUR").toValue(), {
    amount: "0",
    currency: "EUR",
  });
});

test("Money rejects negative, inexact and malformed amounts and currencies", () => {
  for (const amount of ["-1.00", "1e3", "1.", ".5", "", "abc", " 1"]) {
    assert.throws(
      () => Money.of(amount, "EUR"),
      (error: unknown) =>
        error instanceof InvalidMoneyError &&
        error.reason === InvalidMoneyReason.InvalidAmount,
      amount,
    );
  }
  for (const currency of ["eur", "EU", "EURO", ""]) {
    assert.throws(
      () => Money.of("1.00", currency),
      (error: unknown) =>
        error instanceof InvalidMoneyError &&
        error.reason === InvalidMoneyReason.InvalidCurrency,
      currency,
    );
  }
  assert.throws(
    () => Money.of(1 as unknown as string, "EUR"),
    InvalidMoneyError,
  );
});

const menuWithPho = () => {
  const menu = Menu.create({ id: "m-1", restaurantId: "r-1", createdAt: at });
  menu.addItem({
    id: "i-1",
    name: " Pho ",
    description: "  ",
    price: Money.of("9.50", "EUR"),
    isAvailable: true,
    addedAt: at,
  });
  return menu;
};

test("Menu adds items with unique ids and normalized text", () => {
  const menu = menuWithPho();

  assert.deepEqual(menu.findItem("i-1")?.name, "Pho");
  assert.equal(menu.findItem("i-1")?.description, undefined);
  assert.deepEqual(
    menu.pullEvents().map((event) => event.type),
    ["MenuCreated", "MenuItemAdded"],
  );
  assert.throws(
    () =>
      menu.addItem({
        id: "i-1",
        name: "Again",
        price: Money.of("1", "EUR"),
        isAvailable: true,
        addedAt: at,
      }),
    (error: unknown) =>
      error instanceof InvalidMenuError &&
      error.reason === InvalidMenuReason.DuplicateMenuItem,
  );
  assert.throws(
    () =>
      menu.addItem({
        id: "i-2",
        name: " ",
        price: Money.of("1", "EUR"),
        isAvailable: true,
        addedAt: at,
      }),
    (error: unknown) =>
      error instanceof InvalidMenuError &&
      error.reason === InvalidMenuReason.EmptyItemName,
  );
});

test("Menu update records availability changes separately from details", () => {
  const menu = menuWithPho();
  menu.pullEvents();

  assert.equal(
    menu.updateItem("i-1", { price: Money.of("9.5", "EUR") }, at),
    false,
  );
  assert.equal(
    menu.updateItem(
      "i-1",
      { price: Money.of("10.00", "EUR"), isAvailable: false },
      at,
    ),
    true,
  );
  assert.deepEqual(
    menu.pullEvents().map((event) => event.type),
    ["MenuItemUpdated", "MenuItemAvailabilityChanged"],
  );
  assert.equal(menu.findItem("i-1")?.price.amount, "10.00");
  assert.equal(menu.updateItem("i-1", { description: "Beef soup" }, at), true);
  assert.equal(menu.updateItem("i-1", { description: null }, at), true);
  assert.equal(menu.findItem("i-1")?.description, undefined);
  assert.throws(() => menu.updateItem("missing", {}, at), InvalidMenuError);
});

test("Menu availability change is a no-op for the current value", () => {
  const menu = menuWithPho();
  menu.pullEvents();

  assert.equal(menu.setItemAvailability("i-1", true, at), false);
  assert.equal(menu.setItemAvailability("i-1", false, at), true);
  assert.deepEqual(menu.pullEvents(), [
    {
      type: "MenuItemAvailabilityChanged",
      menuId: "m-1",
      restaurantId: "r-1",
      menuItemId: "i-1",
      isAvailable: false,
      occurredAt: at,
    },
  ]);
});

test("Menu snapshot round trip keeps exact prices", () => {
  const menu = menuWithPho();
  const restored = Menu.restore(menu.toSnapshot(), 4);

  assert.equal(restored.version, 4);
  assert.equal(restored.findItem("i-1")?.price.amount, "9.50");
  assert.deepEqual(restored.toSnapshot(), menu.toSnapshot());
  assert.deepEqual(restored.pullEvents(), []);
});
