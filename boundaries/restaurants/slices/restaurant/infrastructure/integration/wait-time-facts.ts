// Consumer of the context's own Restaurant Order facts (ADR 11, ADR 28): Wait
// Time Statistics of the restaurant (FR-644). Accepted opens an order, Ready
// records its duration, Cancelled and PreparationFailed drop it. Events
// published before they carried restaurant_id (FR-656) change nothing.
import { type DescMessage, fromBinary } from "@bufbuild/protobuf";

import {
  PreparationFailedSchema,
  RestaurantOrderAcceptedSchema,
  RestaurantOrderCancelledSchema,
  RestaurantOrderReadySchema,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";
import {
  type Envelope,
  PermanentMessageError,
} from "../../../../pkg/infrastructure/messaging/envelope.ts";
import type {
  ErrorClass,
  EventHandler,
} from "../../../../pkg/infrastructure/messaging/processor.ts";
import type {
  RecordWaitTime,
  RecordWaitTimeHandler,
} from "../../applications/record-wait-time/record-wait-time.ts";

/** Имя обработчика в Inbox и label `handler_name`. */
export const WAIT_TIME_CONSUMER = "restaurants.wait-time";

type OrderFact = { orderId: string; restaurantId: string };

const handlerOf = <Desc extends DescMessage>(
  schema: Desc,
  fact: RecordWaitTime["fact"],
  recordWaitTime: Pick<RecordWaitTimeHandler, "execute">,
): EventHandler => ({
  eventType: schema.typeName,

  async handle(envelope: Envelope, payload: Uint8Array): Promise<void> {
    let event: OrderFact;
    try {
      event = fromBinary(schema, payload) as unknown as OrderFact;
    } catch (error) {
      throw new PermanentMessageError(
        `${schema.name} payload is not a valid Protobuf message`,
        "deserialization",
        { cause: error },
      );
    }
    if (event.restaurantId === "") return;
    if (event.orderId === "") {
      throw new PermanentMessageError(`${schema.name} requires order_id`);
    }
    await recordWaitTime.execute({
      restaurantId: event.restaurantId,
      orderId: event.orderId,
      fact,
      occurredAt: envelope.occurredAt,
    });
  },

  // An unknown restaurant is not fixed by a retry.
  classify(error: unknown): ErrorClass | undefined {
    if (!(error instanceof Error && "code" in error)) return undefined;
    return error.code === "not_found" ? "permanent" : undefined;
  },
});

export const createWaitTimeHandlers = (dependencies: {
  recordWaitTime: Pick<RecordWaitTimeHandler, "execute">;
}): readonly EventHandler[] => [
  handlerOf(
    RestaurantOrderAcceptedSchema,
    "accepted",
    dependencies.recordWaitTime,
  ),
  handlerOf(RestaurantOrderReadySchema, "ready", dependencies.recordWaitTime),
  handlerOf(
    RestaurantOrderCancelledSchema,
    "dropped",
    dependencies.recordWaitTime,
  ),
  handlerOf(PreparationFailedSchema, "dropped", dependencies.recordWaitTime),
];
