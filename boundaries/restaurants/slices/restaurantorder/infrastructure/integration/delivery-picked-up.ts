// Consumer Integration Event `nomnom.delivery.v1.DeliveryPickedUp` (ADR 11,
// ADR 28): Transport DTO → Command Record Pickup. The courier took the order,
// so the kitchen is done with it and the order leaves the kitchen board.

import { DeliveryPickedUpSchema } from "@buf/nomnom_delivery.bufbuild_es/nomnom/delivery/v1/events_pb.js";
import { fromBinary } from "@bufbuild/protobuf";
import {
  type Envelope,
  PermanentMessageError,
} from "../../../../infrastructure/messaging/envelope.ts";
import type {
  ErrorClass,
  EventHandler,
} from "../../../../infrastructure/messaging/processor.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { PickupWitnessAccess } from "../../applications/access-control.ts";
import type { RecordPickupHandler } from "../../applications/record-pickup/record-pickup.ts";

/** Стрим Integration Events delivery (delivery/slices/delivery/infrastructure/integration_events.rs). */
export const DELIVERY_EVENTS_TOPIC = "delivery.v1.delivery-events";
export const DELIVERY_PICKED_UP_EVENT_TYPE = DeliveryPickedUpSchema.typeName;
/** Имя обработчика в Inbox и label `handler_name`. */
export const DELIVERY_PICKED_UP_CONSUMER = "restaurants.delivery-picked-up";

/**
 * Subject событий стрима `delivery.v1.delivery-events`: подлинность события
 * обеспечивает владение topic (пишет только Outbox delivery).
 */
export const DELIVERY_EVENT_SOURCE: Subject = Object.freeze({
  type: "service",
  id: "delivery",
});

/** Record Pickup разрешён только событиям {@link DELIVERY_EVENT_SOURCE}. */
export class DeliveryEventSourceAccessControl implements PickupWitnessAccess {
  async canRecordPickup(subject: Subject): Promise<boolean> {
    return (
      subject.type === DELIVERY_EVENT_SOURCE.type &&
      subject.id === DELIVERY_EVENT_SOURCE.id
    );
  }
}

/** Ошибки, которые повтор того же события не исправит: DLQ. */
const PERMANENT_CODES = new Set([
  "invalid_market_id",
  "market_mismatch",
  "permission_denied",
]);
/**
 * Бизнес-исход: событие подтверждается без изменений, причина в log.
 * `not_found` — заказ не попадал на кухню этого контекста; `invalid_transition`
 * — Pickup раньше `READY` или после отмены: доску меняет только `READY`.
 */
const BUSINESS_CODES = new Set(["not_found", "invalid_transition"]);

export const createDeliveryPickedUpHandler = (dependencies: {
  recordPickup: Pick<RecordPickupHandler, "execute">;
}): EventHandler => ({
  eventType: DELIVERY_PICKED_UP_EVENT_TYPE,

  async handle(envelope: Envelope, payload: Uint8Array): Promise<void> {
    let event: ReturnType<typeof fromBinary<typeof DeliveryPickedUpSchema>>;
    try {
      event = fromBinary(DeliveryPickedUpSchema, payload);
    } catch (error) {
      throw new PermanentMessageError(
        "DeliveryPickedUp payload is not a valid Protobuf message",
        "deserialization",
        { cause: error },
      );
    }
    if (event.orderId === "") {
      throw new PermanentMessageError("DeliveryPickedUp requires order_id");
    }
    await dependencies.recordPickup.execute({
      subject: DELIVERY_EVENT_SOURCE,
      orderId: event.orderId,
      // Empty — издатель до ADR 41: Market не сверяется.
      ...(event.marketId === "" ? {} : { marketId: event.marketId }),
      pickedUpAt: envelope.occurredAt,
    });
  },

  classify(error: unknown): ErrorClass | undefined {
    if (!(error instanceof Error && "code" in error)) return undefined;
    const code = String(error.code);
    if (PERMANENT_CODES.has(code)) return "permanent";
    if (BUSINESS_CODES.has(code)) return "business";
    return undefined;
  },
});
