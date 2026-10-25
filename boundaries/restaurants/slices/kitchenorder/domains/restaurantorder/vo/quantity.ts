export class InvalidQuantityError extends Error {
  readonly code = "invalid_quantity";

  constructor(value: unknown) {
    super(`quantity must be a positive integer, got ${String(value)}`);
    this.name = "InvalidQuantityError";
  }
}

/** Positive integer number of portions of one Preparation Item. */
export class Quantity {
  readonly #value: number;

  private constructor(value: number) {
    this.#value = value;
  }

  static of(value: number): Quantity {
    if (!Number.isSafeInteger(value) || value < 1) {
      throw new InvalidQuantityError(value);
    }
    return new Quantity(value);
  }

  get value(): number {
    return this.#value;
  }

  equals(other: Quantity): boolean {
    return this.#value === other.#value;
  }
}
