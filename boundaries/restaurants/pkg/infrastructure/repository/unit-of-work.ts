import { AsyncLocalStorage } from "node:async_hooks";

import type { ClientSession, MongoClient, TransactionOptions } from "mongodb";

import type { UnitOfWork } from "../../application/unit-of-work.ts";

const TRANSACTION_OPTIONS: TransactionOptions = {
  readConcern: { level: "snapshot" },
  writeConcern: { w: "majority" },
  readPreference: "primary",
};

/**
 * Unit of Work as a MongoDB multi-document transaction (requires a replica
 * set). The session of the active transaction is carried in async context, so
 * repositories and the Outbox join it without changes to the ports. The driver
 * retries `work` on TransientTransactionError and the commit on
 * UnknownTransactionCommitResult, which the port allows. A nested `run`
 * joins the outer transaction.
 */
export class MongoUnitOfWork implements UnitOfWork {
  readonly #client: MongoClient;
  readonly #sessions = new AsyncLocalStorage<ClientSession>();

  constructor(client: MongoClient) {
    this.#client = client;
  }

  /** Session of the active transaction, `undefined` outside `run`. */
  get session(): ClientSession | undefined {
    return this.#sessions.getStore();
  }

  async run<T>(work: () => Promise<T>): Promise<T> {
    if (this.#sessions.getStore() !== undefined) {
      return work();
    }
    const session = this.#client.startSession();
    try {
      return await session.withTransaction(
        () => this.#sessions.run(session, work),
        TRANSACTION_OPTIONS,
      );
    } finally {
      await session.endSession();
    }
  }
}
