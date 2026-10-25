// Consumer Integration Event `nomnom.orders.v1.OrderPlaced` (ADR 11, ADR 28):
// Transport DTO → Command Receive Order Request. Тот же use case вызывает RPC
// ReceiveOrderRequest Temporal Activity orders; оба пути идемпотентны по
// order_id и сходятся к одному Restaurant Order.
import { fromBinary } from "@bufbuild/protobuf";

import { OrderPlacedSchema } from "../../../../infrastructure/grpc/gen/nomnom/orders/v1/events_pb.js";
import type { Subject } from "../../../../pkg/application/subject.ts";
import {
  type Envelope,
  PermanentMessageError,
} from "../../../../pkg/infrastructure/messaging/envelope.ts";
import type {
  ErrorClass,
  EventHandler,
} from "../../../../pkg/infrastructure/messaging/processor.ts";
import type { FulfillmentCoordinatorAccess } from "../../applications/access-control.ts";
import type { ReceiveOrderRequestHandler } from "../../applications/receive-order-request/receive-order-request.ts";

/** Стрим Integration Events orders (orders/infrastructure/postgres/outbox.go). */
export const ORDER_EVENTS_TOPIC = "orders.v1.order-events";
export const ORDER_PLACED_EVENT_TYPE = OrderPlacedSchema.typeName;
/** Имя обработчика в Inbox и label `handler_name`. */
export const ORDER_PLACED_CONSUMER = "restaurants.order-placed";

/**
 * Subject событий стрима `orders.v1.order-events`: подлинность события
 * обеспечивает владение topic (пишет только Outbox orders), а не identity
 * вызывающего.
 */
export const ORDERS_EVENT_SOURCE: Subject = Object.freeze({
  type: "service",
  id: "orders",
});

/**
 * Authorization port Receive Order Request в процессе consumer: разрешает
 * только координацию fulfillment от {@link ORDERS_EVENT_SOURCE}; команды кухни
 * и чтения consumer не выполняет, поэтому других портов доступа адаптер не
 * реализует.
 */
export class OrdersEventSourceAccessControl
  implements FulfillmentCoordinatorAccess
{
  async canCoordinateFulfillment(subject: Subject): Promise<boolean> {
    return (
      subject.type === ORDERS_EVENT_SOURCE.type &&
      subject.id === ORDERS_EVENT_SOURCE.id
    );
  }
}

/** Ошибки Receive Order Request, которые повтор того же события не исправит. */
const PERMANENT_CODES = new Set([
  "invalid_restaurant_order",
  "invalid_quantity",
  "permission_denied",
]);
/** Бизнес-исход: событие подтверждается без Restaurant Order, причина в log. */
const BUSINESS_CODES = new Set(["not_found", "already_exists"]);

export const createOrderPlacedHandler = (dependencies: {
  receiveOrderRequest: Pick<ReceiveOrderRequestHandler, "execute">;
}): EventHandler => ({
  eventType: ORDER_PLACED_EVENT_TYPE,

  async handle(_envelope: Envelope, payload: Uint8Array): Promise<void> {
    let event: ReturnType<typeof fromBinary<typeof OrderPlacedSchema>>;
    try {
      event = fromBinary(OrderPlacedSchema, payload);
    } catch (error) {
      throw new PermanentMessageError(
        "OrderPlaced payload is not a valid Protobuf message",
        "deserialization",
        { cause: error },
      );
    }
    if (event.orderId === "" || event.restaurantId === "") {
      throw new PermanentMessageError(
        "OrderPlaced requires order_id and restaurant_id",
      );
    }
    if (event.items.length === 0) {
      throw new PermanentMessageError(
        `OrderPlaced ${event.orderId} has no items: the publisher predates the items contract`,
      );
    }
    await dependencies.receiveOrderRequest.execute({
      subject: ORDERS_EVENT_SOURCE,
      restaurantId: event.restaurantId,
      orderId: event.orderId,
      items: event.items.map((item) => ({
        menuItemId: item.menuItemId,
        quantity: item.quantity,
      })),
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
