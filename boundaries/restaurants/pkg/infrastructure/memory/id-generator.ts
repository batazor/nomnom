import type { IdGenerator } from "../../application/id-generator.ts";

/** Deterministic identifiers `<prefix>-1`, `<prefix>-2`, ... for tests. */
export class SequentialIdGenerator implements IdGenerator {
  readonly #prefix: string;
  #sequence = 0;

  constructor(prefix: string) {
    this.#prefix = prefix;
  }

  next(): string {
    this.#sequence += 1;
    return `${this.#prefix}-${this.#sequence}`;
  }
}
