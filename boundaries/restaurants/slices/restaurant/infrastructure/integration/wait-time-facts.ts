// Consumer of the context's own Restaurant Order facts (ADR 11, ADR 28): Wait
// Time Statistics of the restaurant (FR-644), measured from joining the
// Kitchen Queue. Accepted with queued_at opens an order queued in the same
// Unit of Work as its Acceptance; a scheduled order is accepted without
// queued_at and its queuing publishes nothing, so Preparation Started opens it
// with its queued_at (a repeat for an already open order changes nothing).
// Ready records the duration, Cancelled and PreparationFailed drop the order.
// Events published before they carried restaurant_id (FR-656) change nothing.
import {
  type DescMessage,
  fromBinary,
  type MessageShape,
} from "@bufbuild/protobuf";
import { type Timestamp, timestampDate } from "@bufbuild/protobuf/wkt";

import {
  PreparationFailedSchema,
  PreparationStartedSchema,
  RestaurantOrderAcceptedSchema,
  RestaurantOrderCancelledSchema,
  RestaurantOrderReadySchema,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import {
  type Envelope,
  PermanentMessageError,
} from "../../../../infrastructure/messaging/envelope.ts";
import type {
  ErrorClass,
  EventHandler,
} from "../../../../infrastructure/messaging/processor.ts";
import type {
  RecordWaitTime,
  RecordWaitTimeHandler,
} from "../../applications/record-wait-time/record-wait-time.ts";

/** Имя обработчика в Inbox и label `handler_name`. */
export const WAIT_TIME_CONSUMER = "restaurants.wait-time";

type OrderFact = { orderId: string; restaurantId: string };

/**
 * What a fact records: `undefined` for a fact that changes nothing (an
 * Accepted of a scheduled order).
 */
type Recorded = Pick<RecordWaitTime, "fact" | "occurredAt"> | undefined;

const handlerOf = <Desc extends DescMessage>(
  schema: Desc,
  record: (event: MessageShape<Desc>, envelope: Envelope) => Recorded,
  recordWaitTime: Pick<RecordWaitTimeHandler, "execute">,
): EventHandler => ({
  eventType: schema.typeName,

  async handle(envelope: Envelope, payload: Uint8Array): Promise<void> {
    let event: MessageShape<Desc>;
    try {
      event = fromBinary(schema, payload);
    } catch (error) {
      throw new PermanentMessageError(
        `${schema.name} payload is not a valid Protobuf message`,
        "deserialization",
        { cause: error },
      );
    }
    const fact = event as unknown as OrderFact;
    if (fact.restaurantId === "") return;
    if (fact.orderId === "") {
      throw new PermanentMessageError(`${schema.name} requires order_id`);
    }
    const recorded = record(event, envelope);
    if (recorded === undefined) return;
    await recordWaitTime.execute({
      restaurantId: fact.restaurantId,
      orderId: fact.orderId,
      ...recorded,
    });
  },

  // An unknown restaurant is not fixed by a retry.
  classify(error: unknown): ErrorClass | undefined {
    if (!(error instanceof Error && "code" in error)) return undefined;
    return error.code === "not_found" ? "permanent" : undefined;
  },
});

const queuedAt = (
  name: string,
  value: Timestamp | undefined,
): Pick<RecordWaitTime, "fact" | "occurredAt"> => {
  if (value === undefined) {
    throw new PermanentMessageError(`${name} requires queued_at`);
  }
  return { fact: "queued", occurredAt: timestampDate(value) };
};

export const createWaitTimeHandlers = (dependencies: {
  recordWaitTime: Pick<RecordWaitTimeHandler, "execute">;
}): readonly EventHandler[] => [
  // A scheduled order is accepted without queued_at: not open yet.
  handlerOf(
    RestaurantOrderAcceptedSchema,
    (event) =>
      event.queuedAt === undefined
        ? undefined
        : { fact: "queued", occurredAt: timestampDate(event.queuedAt) },
    dependencies.recordWaitTime,
  ),
  handlerOf(
    PreparationStartedSchema,
    (event) => queuedAt(PreparationStartedSchema.name, event.queuedAt),
    dependencies.recordWaitTime,
  ),
  handlerOf(
    RestaurantOrderReadySchema,
    (_event, envelope) => ({ fact: "ready", occurredAt: envelope.occurredAt }),
    dependencies.recordWaitTime,
  ),
  handlerOf(
    RestaurantOrderCancelledSchema,
    (_event, envelope) => ({
      fact: "dropped",
      occurredAt: envelope.occurredAt,
    }),
    dependencies.recordWaitTime,
  ),
  handlerOf(
    PreparationFailedSchema,
    (_event, envelope) => ({
      fact: "dropped",
      occurredAt: envelope.occurredAt,
    }),
    dependencies.recordWaitTime,
  ),
];
