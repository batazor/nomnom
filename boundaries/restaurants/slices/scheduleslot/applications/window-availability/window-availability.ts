import type { Restaurant } from "../../../restaurant/domains/restaurant/restaurant.ts";
import type {
  DeliveryWindow,
  DeliveryWindows,
  ScheduleSlotRepository,
} from "../../domains/scheduleslot/scheduleslot.ts";

/** An offered Delivery Window with room for one more reservation or not. */
export type WindowAvailability = Readonly<{
  window: DeliveryWindow;
  available: boolean;
}>;

/**
 * Offered windows of the restaurant at `now` with their availability under
 * its current capacity (FR-673): one read of the saved windows in the range;
 * a window never written is free. Empty without settings or while the
 * restaurant does not accept orders.
 */
export const windowAvailability = async (
  dependencies: Readonly<{
    windows: DeliveryWindows;
    slots: Pick<ScheduleSlotRepository, "listBetween">;
  }>,
  restaurant: Restaurant,
  now: Date,
): Promise<readonly WindowAvailability[]> => {
  const settings = restaurant.scheduledOrders;
  const offered = dependencies.windows.offered(restaurant, now);
  const first = offered.at(0);
  const last = offered.at(-1);
  if (settings === undefined || first === undefined || last === undefined) {
    return [];
  }
  const saved = new Map(
    (
      await dependencies.slots.listBetween(
        restaurant.id,
        first.start,
        last.start,
      )
    ).map((slot) => [slot.window.start.getTime(), slot]),
  );
  return offered.map((window) => {
    const slot = saved.get(window.start.getTime());
    return {
      window,
      available:
        slot === undefined || slot.isAvailable(now, settings.slotCapacity),
    };
  });
};
