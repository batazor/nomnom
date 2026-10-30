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

/**
 * Change Notification (ADR 36): the GraphQL entity key the BFF Router turns
 * into a subscription event. It carries no state; subscribers re-read the
 * entity with their own rights.
 */
export type ChangeNotification = Readonly<{
  /** event_type header of the record, e.g. KitchenChanged. */
  type: string;
  topic: string;
  /** JSON entity with `__typename` and its key fields. */
  entity: Readonly<Record<string, string>>;
  occurredAt: Date;
}>;

/** Writes Change Notifications in the active Unit of Work, like the Outbox. */
export interface ChangeNotifications {
  notify(
    aggregate: AggregateRef,
    notification: ChangeNotification,
  ): Promise<void>;
}
