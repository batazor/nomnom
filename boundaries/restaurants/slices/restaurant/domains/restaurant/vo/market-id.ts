/** Slug of a Market (ADR 41): `berlin`, `new-york`. */
const MARKET_ID_PATTERN = /^[a-z][a-z0-9-]{1,31}$/;

export class InvalidMarketIdError extends Error {
  readonly code = "invalid_market_id";

  constructor(value: string) {
    super(
      `market_id must match ${MARKET_ID_PATTERN.source}, got ${JSON.stringify(value)}`,
    );
    this.name = "InvalidMarketIdError";
  }
}

/**
 * Market of an aggregate (ADR 41): the city whose restaurants, couriers and
 * orders are served together by one Cell. Set at creation, never changed.
 */
export class MarketId {
  readonly #value: string;

  private constructor(value: string) {
    this.#value = value;
  }

  static of(value: string): MarketId {
    if (!MARKET_ID_PATTERN.test(value)) {
      throw new InvalidMarketIdError(value);
    }
    return new MarketId(value);
  }

  get value(): string {
    return this.#value;
  }

  equals(other: MarketId): boolean {
    return this.#value === other.#value;
  }

  toString(): string {
    return this.#value;
  }
}
