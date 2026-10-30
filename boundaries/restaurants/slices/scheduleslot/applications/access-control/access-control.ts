import type { Subject } from "../../../../pkg/authz/subject.ts";

/**
 * Authorization port of the `scheduleslot` slice. Slot Reservations are made,
 * confirmed and released by the `service` identity of the `orders`
 * fulfillment process only (FR-674, FR-675): the same platform capability as
 * Receive Order Request and Cancel Restaurant Order, checked before any read
 * because Confirm and Release name only the order. The implementation throws
 * when no decision can be obtained (fail closed).
 */
export interface SlotReservationAccess {
  canCoordinateFulfillment(subject: Subject): Promise<boolean>;
}
