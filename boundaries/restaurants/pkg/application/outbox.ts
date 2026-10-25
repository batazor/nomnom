/** Reference to the aggregate whose committed version produced the events. */
export type AggregateRef = Readonly<{
  type: string;
  id: string;
  version: number;
}>;

/** Minimal shape of a Domain Event that a use case decided to publish. */
export type PublishableEvent = Readonly<{
  type: string;
  occurredAt: Date;
}>;

/**
 * Transactional Outbox port. It MUST be called inside an active Unit of Work
 * together with the state change. Infrastructure maps events to Integration
 * Events, fills the Event Envelope and serializes them.
 */
export interface Outbox {
  add(
    aggregate: AggregateRef,
    events: readonly PublishableEvent[],
  ): Promise<void>;
}
