/**
 * Batch reads of public data (ADR 7): one call serves the ids of a whole page,
 * so the number of calls of the caller does not grow with the page.
 */

export const MAX_BATCH_SIZE = 100;

export class InvalidBatchError extends Error {
  readonly code = "invalid_batch";

  constructor(size: number) {
    super(`a batch must have 1..${MAX_BATCH_SIZE} ids, got ${size}`);
    this.name = "InvalidBatchError";
  }
}

/** The distinct ids of a batch; empty or more than 100 is InvalidBatchError. */
export const batchIds = (ids: readonly string[]): readonly string[] => {
  if (ids.length === 0 || ids.length > MAX_BATCH_SIZE) {
    throw new InvalidBatchError(ids.length);
  }
  return [...new Set(ids)];
};
