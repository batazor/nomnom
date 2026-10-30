/**
 * Local transaction boundary of one use case inside the `restaurants` storage.
 *
 * `work` may be executed again by an implementation that retries transient
 * failures, so it MUST NOT perform external side effects. A nested `run`
 * participates in the outer scope.
 */
export interface UnitOfWork {
  run<T>(work: () => Promise<T>): Promise<T>;
}
