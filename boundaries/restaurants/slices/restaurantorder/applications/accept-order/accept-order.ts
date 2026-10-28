import type { Clock } from "../../../../pkg/application/clock.ts";
import { NotFoundError } from "../../../../pkg/application/errors.ts";
import type { Outbox } from "../../../../pkg/application/outbox.ts";
import { requirePermission } from "../../../../pkg/application/require-permission.ts";
import type { UnitOfWork } from "../../../../pkg/application/unit-of-work.ts";
import {
  type AcceptanceConditions,
  evaluateOrderAcceptance,
} from "../../domains/restaurantorder/acceptance.ts";
import type {
  PreparationItem,
  RestaurantOrderRepository,
} from "../../domains/restaurantorder/restaurantorder.ts";
import type { KitchenStaffAccess } from "../access-control.ts";
import type { KitchenCommand } from "../kitchen-command.ts";
import { loadRestaurantOrder } from "../load-restaurant-order.ts";
import { saveAndPublish } from "../publish-order-change.ts";
import {
  type RestaurantOrderView,
  toRestaurantOrderView,
} from "../restaurant-order-view.ts";

/**
 * `expectedReadyAt` is the ready time named by the kitchen; without it the
 * {@link ReadyTimeEstimator} computes it (FR-609). A scheduled order takes
 * none: it is ready at its Planned Ready At (FR-222).
 */
export type AcceptOrder = KitchenCommand & Readonly<{ expectedReadyAt?: Date }>;

/**
 * Facts of the restaurant and its menu that Acceptance needs: whether the
 * restaurant is open and the availability of the requested Menu Items. An item
 * missing from the menu (or a restaurant without a menu) has no entry.
 */
export type AcceptanceFacts = Readonly<{
  isOpen: boolean;
  available: ReadonlyMap<string, boolean>;
}>;

/**
 * Reads {@link AcceptanceFacts} inside the Unit of Work; `undefined` when the
 * restaurant does not exist. It reads only local `restaurants` data.
 */
export interface AcceptanceFactsReader {
  read(
    restaurantId: string,
    menuItemIds: readonly string[],
  ): Promise<AcceptanceFacts | undefined>;
}

/**
 * Supplies the out-of-state Acceptance conditions (opening hours, schedule
 * exceptions, capacity, item windows and remaining capacity) at `now`. The
 * model of these data is not agreed yet. It is called inside the Unit of Work,
 * so it MUST read only local `restaurants` data and perform no external calls.
 */
export interface AcceptanceConditionsProvider {
  load(input: {
    restaurantId: string;
    menuItemIds: readonly string[];
    now: Date;
  }): Promise<AcceptanceConditions>;
}

/**
 * Computes the initial `expected_ready_at`; the calculation is an open
 * product question. Called inside the Unit of Work with the same constraints
 * as {@link AcceptanceConditionsProvider}.
 */
export interface ReadyTimeEstimator {
  estimate(input: {
    restaurantId: string;
    items: readonly PreparationItem[];
    acceptedAt: Date;
  }): Promise<Date>;
}

/**
 * Accepts a `RECEIVED` order of an open restaurant whose items are available.
 * An order without a Delivery Window gets the kitchen's or the estimated
 * `expectedReadyAt` (later than now, otherwise `invalid_restaurant_order`)
 * and is queued in the same Unit of Work. A scheduled order is accepted at
 * its Planned Ready At and stays `ACCEPTED` until `orders` queues it at its
 * planned start (FR-222, FR-224). Only `RestaurantOrderAccepted` is
 * published; `RestaurantOrderQueued` stays local.
 */
export class AcceptOrderHandler {
  readonly #accessControl: KitchenStaffAccess;
  readonly #unitOfWork: UnitOfWork;
  readonly #restaurantOrders: RestaurantOrderRepository;
  readonly #facts: AcceptanceFactsReader;
  readonly #conditions: AcceptanceConditionsProvider;
  readonly #readyTimeEstimator: ReadyTimeEstimator;
  readonly #outbox: Outbox;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: KitchenStaffAccess;
    unitOfWork: UnitOfWork;
    restaurantOrders: RestaurantOrderRepository;
    facts: AcceptanceFactsReader;
    conditions: AcceptanceConditionsProvider;
    readyTimeEstimator: ReadyTimeEstimator;
    outbox: Outbox;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#unitOfWork = dependencies.unitOfWork;
    this.#restaurantOrders = dependencies.restaurantOrders;
    this.#facts = dependencies.facts;
    this.#conditions = dependencies.conditions;
    this.#readyTimeEstimator = dependencies.readyTimeEstimator;
    this.#outbox = dependencies.outbox;
    this.#clock = dependencies.clock;
  }

  async execute(command: AcceptOrder): Promise<RestaurantOrderView> {
    await requirePermission(
      this.#accessControl.canOperateKitchen(
        command.subject,
        command.restaurantId,
      ),
      "accept restaurant order",
    );
    const now = this.#clock.now();
    return this.#unitOfWork.run(async () => {
      const order = await loadRestaurantOrder(
        this.#restaurantOrders,
        command.restaurantId,
        command.restaurantOrderId,
      );
      const menuItemIds = [
        ...new Set(order.items.map((item) => item.menuItemId)),
      ];
      const facts = await this.#facts.read(order.restaurantId, menuItemIds);
      if (facts === undefined) {
        throw new NotFoundError("Restaurant", order.restaurantId);
      }
      const conditions = await this.#conditions.load({
        restaurantId: order.restaurantId,
        menuItemIds,
        now,
      });
      const eligibility = evaluateOrderAcceptance({
        now,
        restaurant: { isOpen: facts.isOpen },
        menu: {
          findItem: (menuItemId) => {
            const isAvailable = facts.available.get(menuItemId);
            return isAvailable === undefined ? undefined : { isAvailable };
          },
        },
        items: order.items.map((item) => ({
          menuItemId: item.menuItemId,
          quantity: item.quantity.value,
        })),
        conditions,
      });
      // The kitchen names the time or the Ready Time Estimator computes it;
      // a scheduled order is ready at its Planned Ready At.
      const expectedReadyAt =
        order.schedule !== undefined || command.expectedReadyAt !== undefined
          ? command.expectedReadyAt
          : await this.#readyTimeEstimator.estimate({
              restaurantId: order.restaurantId,
              items: order.items,
              acceptedAt: now,
            });
      order.accept({ acceptedAt: now, expectedReadyAt, eligibility });
      if (order.schedule === undefined) {
        order.queue({ queuedAt: now });
      }
      const version = await saveAndPublish(
        this.#restaurantOrders,
        this.#outbox,
        order,
        ["RestaurantOrderAccepted"],
      );
      return toRestaurantOrderView(order, version);
    });
  }
}
