import { AsyncLocalStorage } from "node:async_hooks";

import { type Meter, metrics } from "@opentelemetry/api";
import {
  type ClientSession,
  type MongoClient,
  MongoServerError,
  type TransactionOptions,
} from "mongodb";

import type { UnitOfWork } from "../../application/unit-of-work.ts";

const TRANSACTION_OPTIONS: TransactionOptions = {
  readConcern: { level: "snapshot" },
  writeConcern: { w: "majority" },
  readPreference: "primary",
};

export const UOW_INSTRUMENTATION = "nomnom.restaurants.uow";

/** Seconds: one transaction, the driver's retries included. */
export const UOW_DURATION_BUCKETS = [
  0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30,
];

/** Server code of a write conflict between concurrent transactions. */
const WRITE_CONFLICT_CODE = 112;

// Attributes from the registry ops/semconv/model/uow.yaml (make semconv-check).
const OUTCOME = "nomnom.outcome";
const REASON = "nomnom.reason";

export type UnitOfWorkOutcome = "committed" | "rolled_back" | "commit_failed";

/**
 * Unit of Work as a MongoDB multi-document transaction (requires a replica
 * set). The session of the active transaction is carried in async context, so
 * repositories and the Outbox join it without changes to the ports. The driver
 * retries `work` on TransientTransactionError and the commit on
 * UnknownTransactionCommitResult, which the port allows. A nested `run`
 * joins the outer transaction.
 *
 * Telemetry (ADR 20, ops/semconv/model/uow.yaml): the outermost `run` records
 * `nomnom.uow.transactions` and `nomnom.uow.duration` by outcome — `rolled_back`
 * when `work` failed and the transaction was aborted, `commit_failed` when
 * `work` succeeded and the commit did not — and every repeated call of `work`
 * by the driver as `nomnom.uow.retries`. The reason is the error of the
 * previous attempt: `write_conflict` for WriteConflict, otherwise `other`
 * (MongoDB has no serialization failures or deadlocks). Instruments live on
 * the global MeterProvider by default and are no-op until telemetry starts.
 */
export class DatabaseUnitOfWork implements UnitOfWork {
  readonly #client: MongoClient;
  readonly #sessions = new AsyncLocalStorage<ClientSession>();
  readonly #transactions;
  readonly #duration;
  readonly #retries;

  constructor(
    client: MongoClient,
    meter: Meter = metrics.getMeter(UOW_INSTRUMENTATION),
  ) {
    this.#client = client;
    this.#transactions = meter.createCounter("nomnom.uow.transactions", {
      description: "Unit of Work transactions by outcome.",
      unit: "{transaction}",
    });
    this.#duration = meter.createHistogram("nomnom.uow.duration", {
      description: "Duration of one Unit of Work transaction.",
      unit: "s",
      advice: { explicitBucketBoundaries: UOW_DURATION_BUCKETS },
    });
    this.#retries = meter.createCounter("nomnom.uow.retries", {
      description: "Unit of Work retries by conflict class.",
      unit: "{retry}",
    });
  }

  /** Session of the active transaction, `undefined` outside `run`. */
  get session(): ClientSession | undefined {
    return this.#sessions.getStore();
  }

  async run<T>(work: () => Promise<T>): Promise<T> {
    if (this.#sessions.getStore() !== undefined) {
      return work();
    }
    const started = performance.now();
    let attempts = 0;
    // Outcome of `work` in the latest attempt: its error, or `undefined`.
    let failure: { error: unknown } | undefined;
    let outcome: UnitOfWorkOutcome = "committed";
    const session = this.#client.startSession();
    try {
      return await session.withTransaction(async () => {
        if (attempts > 0) {
          this.#retries.add(1, { [REASON]: retryReason(failure) });
        }
        attempts += 1;
        failure = undefined;
        try {
          return await this.#sessions.run(session, work);
        } catch (error) {
          failure = { error };
          throw error;
        }
      }, TRANSACTION_OPTIONS);
    } catch (error) {
      // The driver may wrap the last error of `work` in a timeout error.
      outcome = failure === undefined ? "commit_failed" : "rolled_back";
      throw error;
    } finally {
      await session.endSession();
      const attributes = { [OUTCOME]: outcome };
      this.#transactions.add(1, attributes);
      this.#duration.record((performance.now() - started) / 1000, attributes);
    }
  }
}

const retryReason = (failure: { error: unknown } | undefined): string =>
  failure?.error instanceof MongoServerError &&
  failure.error.code === WRITE_CONFLICT_CODE
    ? "write_conflict"
    : "other";
