import type { Outbox, PublishableEvent } from "./outbox.ts";

/** Aggregate that records Domain Events until they are pulled. */
export type RecordingAggregate = {
  readonly id: string;
  pullEvents(): readonly PublishableEvent[];
};

/**
 * Saves the aggregate and passes the facts named in `published` to the
 * Outbox; local facts are discarded. Without `published` every recorded fact
 * leaves the context. Nothing is added when no fact is published. Must run
 * inside the active Unit of Work. Returns the committed version.
 */
export const publishAggregateChange = async <A extends RecordingAggregate>(
  repository: { save(aggregate: A): Promise<number> },
  outbox: Outbox,
  aggregateType: string,
  published: readonly string[] | undefined,
  aggregate: A,
): Promise<number> => {
  const version = await repository.save(aggregate);
  const events = aggregate
    .pullEvents()
    .filter(
      (event) => published === undefined || published.includes(event.type),
    );
  if (events.length > 0) {
    await outbox.add(
      { type: aggregateType, id: aggregate.id, version },
      events,
    );
  }
  return version;
};
