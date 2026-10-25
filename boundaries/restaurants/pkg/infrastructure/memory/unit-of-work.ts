import type { UnitOfWork } from "../../application/unit-of-work.ts";

/** In-memory storage that can take part in an {@link InMemoryUnitOfWork}. */
export interface TransactionalResource {
  snapshot(): unknown;
  restore(snapshot: unknown): void;
}

/**
 * Test Unit of Work: snapshots every enlisted resource when the outermost scope
 * starts and restores them when the work throws. Nested scopes join the outer
 * scope. It does not retry and does not isolate concurrent transactions.
 */
export class InMemoryUnitOfWork implements UnitOfWork {
  readonly #resources: TransactionalResource[] = [];
  #depth = 0;
  #commits = 0;
  #rollbacks = 0;

  enlist(...resources: TransactionalResource[]): this {
    this.#resources.push(...resources);
    return this;
  }

  get active(): boolean {
    return this.#depth > 0;
  }

  get commits(): number {
    return this.#commits;
  }

  get rollbacks(): number {
    return this.#rollbacks;
  }

  async run<T>(work: () => Promise<T>): Promise<T> {
    if (this.#depth > 0) {
      return work();
    }
    const snapshots = this.#resources.map((resource) => resource.snapshot());
    this.#depth = 1;
    try {
      const result = await work();
      this.#commits += 1;
      return result;
    } catch (error) {
      this.#resources.forEach((resource, index) => {
        resource.restore(snapshots[index]);
      });
      this.#rollbacks += 1;
      throw error;
    } finally {
      this.#depth = 0;
    }
  }
}
