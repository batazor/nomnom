import type { Inbox, InboxRecord } from "../messaging/processor.ts";
import type {
  InMemoryUnitOfWork,
  TransactionalResource,
} from "./unit-of-work.ts";

/**
 * Test Inbox: the record and the changes of `work` commit or roll back
 * together in the enlisted {@link InMemoryUnitOfWork}.
 */
export class InMemoryInbox implements Inbox, TransactionalResource {
  readonly #unitOfWork: InMemoryUnitOfWork;
  #records = new Map<string, InboxRecord>();

  constructor(unitOfWork: InMemoryUnitOfWork) {
    this.#unitOfWork = unitOfWork;
    unitOfWork.enlist(this);
  }

  get records(): readonly InboxRecord[] {
    return [...this.#records.values()];
  }

  async runOnce(
    record: InboxRecord,
    work: () => Promise<void>,
  ): Promise<"processed" | "duplicate"> {
    return this.#unitOfWork.run(async () => {
      const key = `${record.consumer}:${record.eventId}`;
      if (this.#records.has(key)) return "duplicate";
      this.#records.set(key, record);
      await work();
      return "processed" as const;
    });
  }

  snapshot(): unknown {
    return new Map(this.#records);
  }

  restore(snapshot: unknown): void {
    this.#records = new Map(snapshot as Map<string, InboxRecord>);
  }
}
