import { ConcurrencyConflictError } from "../../application/errors.ts";
import type { TransactionalResource } from "./unit-of-work.ts";

export type StoredDocument<T> = Readonly<{
  id: string;
  version: number;
  document: T;
}>;

/**
 * Versioned in-memory document collection with optimistic concurrency.
 * Documents are deep-copied on every read and write, so callers never share
 * mutable state with the store.
 */
export class InMemoryDocumentStore<T> implements TransactionalResource {
  readonly #aggregateType: string;
  #documents = new Map<string, StoredDocument<T>>();

  constructor(aggregateType: string) {
    this.#aggregateType = aggregateType;
  }

  get(id: string): StoredDocument<T> | undefined {
    const stored = this.#documents.get(id);
    return stored === undefined ? undefined : structuredClone(stored);
  }

  all(): StoredDocument<T>[] {
    return [...this.#documents.values()].map((stored) =>
      structuredClone(stored),
    );
  }

  /**
   * Writes `document` if the stored version equals `expectedVersion`
   * (`0` for a new document) and returns the new version.
   */
  save(id: string, expectedVersion: number, document: T): number {
    const currentVersion = this.#documents.get(id)?.version ?? 0;
    if (currentVersion !== expectedVersion) {
      throw new ConcurrencyConflictError(this.#aggregateType, id);
    }
    const version = currentVersion + 1;
    this.#documents.set(id, {
      id,
      version,
      document: structuredClone(document),
    });
    return version;
  }

  snapshot(): unknown {
    return structuredClone(this.#documents);
  }

  restore(snapshot: unknown): void {
    this.#documents = structuredClone(
      snapshot as Map<string, StoredDocument<T>>,
    );
  }
}
