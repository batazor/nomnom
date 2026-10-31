/** Generates identifiers of new aggregates before a Unit of Work starts. */
export interface IdGenerator {
  next(): string;
}
