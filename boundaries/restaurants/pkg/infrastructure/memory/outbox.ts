import type {
  AggregateRef,
  Outbox,
  PublishableEvent,
} from "../../application/outbox.ts";
import type {
  InMemoryUnitOfWork,
  TransactionalResource,
} from "./unit-of-work.ts";

export type InMemoryOutboxRecord = Readonly<{
  aggregate: AggregateRef;
  event: PublishableEvent;
}>;

export class OutboxOutsideUnitOfWorkError extends Error {
  readonly code = "outbox_outside_unit_of_work";

  constructor() {
    super("outbox records can only be added inside an active unit of work");
    this.name = "OutboxOutsideUnitOfWorkError";
  }
}

/** Outbox that accepts records only inside an active in-memory Unit of Work. */
export class InMemoryOutbox implements Outbox, TransactionalResource {
  readonly #unitOfWork: InMemoryUnitOfWork;
  #records: InMemoryOutboxRecord[] = [];

  constructor(unitOfWork: InMemoryUnitOfWork) {
    this.#unitOfWork = unitOfWork;
    unitOfWork.enlist(this);
  }

  get records(): readonly InMemoryOutboxRecord[] {
    return this.#records;
  }

  eventTypes(): string[] {
    return this.#records.map((record) => record.event.type);
  }

  async add(
    aggregate: AggregateRef,
    events: readonly PublishableEvent[],
  ): Promise<void> {
    if (!this.#unitOfWork.active) {
      throw new OutboxOutsideUnitOfWorkError();
    }
    for (const event of events) {
      this.#records.push(structuredClone({ aggregate, event }));
    }
  }

  snapshot(): unknown {
    return [...this.#records];
  }

  restore(snapshot: unknown): void {
    this.#records = [...(snapshot as InMemoryOutboxRecord[])];
  }
}
