import type {
  AcceptanceConditionsProvider,
  ReadyTimeEstimator,
} from "../../applications/accept-order/accept-order.ts";
import type { AcceptanceConditions } from "../../domains/restaurantorder/acceptance.ts";

const BEGINNING = new Date(0);
const END = new Date(8.64e15);

/**
 * MVP Acceptance conditions until opening hours, capacity and item windows
 * have a model: no schedule or capacity limit, so Acceptance depends only on
 * `Restaurant.isOpen` and `MenuItem.isAvailable`. Reads nothing and performs
 * no external calls, as required inside the Unit of Work.
 */
export class UnconstrainedAcceptanceConditions
  implements AcceptanceConditionsProvider
{
  async load(input: {
    menuItemIds: readonly string[];
  }): Promise<AcceptanceConditions> {
    const menuItems: Record<string, AcceptanceConditions["menuItems"][string]> =
      {};
    for (const menuItemId of input.menuItemIds) {
      menuItems[menuItemId] = {
        availableFrom: BEGINNING,
        availableUntil: END,
        remainingCapacity: Number.MAX_SAFE_INTEGER,
      };
    }
    return {
      restaurant: {
        opensAt: BEGINNING,
        closesAt: END,
        currentOrders: 0,
        maximumOrders: Number.MAX_SAFE_INTEGER,
        scheduleException: false,
      },
      menuItems,
    };
  }
}

/**
 * MVP estimate of the initial `expected_ready_at`: a fixed preparation time
 * after Acceptance. Staff move it later with Report Preparation Delay.
 */
export class FixedPreparationTimeEstimator implements ReadyTimeEstimator {
  readonly #milliseconds: number;

  constructor(milliseconds: number) {
    if (!Number.isFinite(milliseconds) || milliseconds <= 0) {
      throw new Error("preparation time must be a positive duration");
    }
    this.#milliseconds = milliseconds;
  }

  async estimate(input: { acceptedAt: Date }): Promise<Date> {
    return new Date(input.acceptedAt.getTime() + this.#milliseconds);
  }
}
