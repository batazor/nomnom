import type { Decimal as DecimalValue } from "decimal.js";
import * as decimalModule from "decimal.js";

// decimal.js ships CommonJS-style typings for its ESM build, so the type
// checker and Node.js disagree on the shape of the default export. At runtime
// the ESM default export is the Decimal class itself.
const Decimal = (decimalModule as unknown as { default: typeof DecimalValue })
  .default;

const AMOUNT_PATTERN = /^\d+(\.\d+)?$/;
const CURRENCY_PATTERN = /^[A-Z]{3}$/;

export const InvalidMoneyReason = {
  InvalidAmount: "invalid_amount",
  InvalidCurrency: "invalid_currency",
} as const;

export type InvalidMoneyReason =
  (typeof InvalidMoneyReason)[keyof typeof InvalidMoneyReason];

export class InvalidMoneyError extends Error {
  readonly code = "invalid_money";
  readonly reason: InvalidMoneyReason;

  constructor(reason: InvalidMoneyReason) {
    super(`invalid money: ${reason}`);
    this.name = "InvalidMoneyError";
    this.reason = reason;
  }
}

export type MoneyValue = Readonly<{ amount: string; currency: string }>;

/**
 * Exact non-negative decimal amount with an ISO 4217-style currency code.
 * Created only from a decimal string, never from a JavaScript `number`.
 */
export class Money {
  readonly #amount: DecimalValue;
  readonly #scale: number;
  readonly #currency: string;

  private constructor(amount: DecimalValue, scale: number, currency: string) {
    this.#amount = amount;
    this.#scale = scale;
    this.#currency = currency;
  }

  /** `amount` uses the form `123` or `123.45`; negative amounts are rejected. */
  static of(amount: string, currency: string): Money {
    if (typeof amount !== "string" || !AMOUNT_PATTERN.test(amount)) {
      throw new InvalidMoneyError(InvalidMoneyReason.InvalidAmount);
    }
    if (typeof currency !== "string" || !CURRENCY_PATTERN.test(currency)) {
      throw new InvalidMoneyError(InvalidMoneyReason.InvalidCurrency);
    }
    const scale = amount.includes(".")
      ? amount.length - amount.indexOf(".") - 1
      : 0;
    return new Money(new Decimal(amount), scale, currency);
  }

  static from(value: MoneyValue): Money {
    return Money.of(value.amount, value.currency);
  }

  /**
   * Decimal string with the scale it was created with (`0.10` stays `0.10`),
   * without exponent and without binary rounding.
   */
  get amount(): string {
    return this.#amount.toFixed(this.#scale);
  }

  get currency(): string {
    return this.#currency;
  }

  equals(other: Money): boolean {
    return (
      this.#currency === other.#currency && this.#amount.equals(other.#amount)
    );
  }

  toValue(): MoneyValue {
    return { amount: this.amount, currency: this.#currency };
  }
}
