// Shared building blocks of the Domain Event → Integration Event mappers of the
// slices (ADR 25, ADR 31): Protobuf serialization of one contract and the
// dispatch of an Outbox record to the mapper of its aggregate type.
import {
  create,
  type DescMessage,
  type MessageInitShape,
  toBinary,
} from "@bufbuild/protobuf";

import type {
  AggregateRef,
  PublishableEvent,
} from "../../pkg/outbox/outbox.ts";
import type {
  OutboxEventMapper,
  OutboxIntegrationEvent,
} from "../repository/outbox.ts";

/**
 * Builder of Integration Events of one stream: `event_type` is the full
 * Protobuf message name, the payload is the serialized contract without a
 * wrapper.
 */
export const protobufIntegrationEvents =
  (topic: string) =>
  <Desc extends DescMessage>(
    schema: Desc,
    message: MessageInitShape<Desc>,
  ): OutboxIntegrationEvent => ({
    eventType: schema.typeName,
    topic,
    payload: toBinary(schema, create(schema, message)),
  });

/**
 * Explicit allowlist of one aggregate: Domain Event type → Integration Event.
 * A type without an entry is not published.
 */
export type EventAllowlist<Event extends PublishableEvent> = {
  readonly [T in Event["type"]]?: (
    event: Extract<Event, { type: T }>,
  ) => OutboxIntegrationEvent;
};

/**
 * Mapper of one aggregate type over its allowlist: events of other aggregates
 * and types without an entry (including inherited keys such as `toString`)
 * are skipped.
 */
export const allowlistMapper =
  <Event extends PublishableEvent>(
    aggregateType: string,
    allowlist: EventAllowlist<Event>,
  ): OutboxEventMapper =>
  (aggregate: AggregateRef, event: PublishableEvent) => {
    if (aggregate.type !== aggregateType) return undefined;
    if (!Object.hasOwn(allowlist, event.type)) return undefined;
    const map = allowlist[event.type as Event["type"]] as
      | ((event: Event) => OutboxIntegrationEvent)
      | undefined;
    return map?.(event as Event);
  };

/**
 * Outbox mapper of the process: dispatches by `aggregate_type` to the mapper
 * of the slice that owns the aggregate; an unknown aggregate type is not
 * published. Each slice keeps its own topic, so ordering by `aggregate_id`
 * holds within every stream.
 */
export const outboxEventMapperByAggregate = (
  mappers: Readonly<Record<string, OutboxEventMapper>>,
): OutboxEventMapper => {
  const byType = new Map(Object.entries(mappers));
  return (aggregate, event) => byType.get(aggregate.type)?.(aggregate, event);
};
