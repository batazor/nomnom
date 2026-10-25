import type { Clock } from "../../../../pkg/application/clock.ts";
import { NotFoundError } from "../../../../pkg/application/errors.ts";
import type { Outbox } from "../../../../pkg/application/outbox.ts";
import type { UnitOfWork } from "../../../../pkg/application/unit-of-work.ts";
import type { MenuRepository } from "../../../menu/domains/menu/menu.ts";
import type { RestaurantRepository } from "../../../restaurant/domains/restaurant/restaurant.ts";
import {
  type AcceptanceConditions,
  evaluateOrderAcceptance,
} from "../../domains/restaurantorder/acceptance.ts";
import type {
  PreparationItem,
  RestaurantOrderRepository,
} from "../../domains/restaurantorder/restaurantorder.ts";
import type { KitchenAccessControl } from "../access-control.ts";
import {
  authorize,
  type KitchenCommand,
  loadRestaurantOrder,
  type RestaurantOrderView,
  saveAndPublish,
  toRestaurantOrderView,
} from "../restaurant-order-support.ts";

export type AcceptOrder = KitchenCommand;

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
 * Accepts a `RECEIVED` order of an open restaurant whose items are available,
 * then queues it in the same Unit of Work (MVP). Only
 * `RestaurantOrderAccepted` is published; `RestaurantOrderQueued` stays local.
 */
export class AcceptOrderHandler {
  readonly #accessControl: KitchenAccessControl;
  readonly #unitOfWork: UnitOfWork;
  readonly #restaurantOrders: RestaurantOrderRepository;
  readonly #restaurants: RestaurantRepository;
  readonly #menus: MenuRepository;
  readonly #conditions: AcceptanceConditionsProvider;
  readonly #readyTimeEstimator: ReadyTimeEstimator;
  readonly #outbox: Outbox;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: KitchenAccessControl;
    unitOfWork: UnitOfWork;
    restaurantOrders: RestaurantOrderRepository;
    restaurants: RestaurantRepository;
    menus: MenuRepository;
    conditions: AcceptanceConditionsProvider;
    readyTimeEstimator: ReadyTimeEstimator;
    outbox: Outbox;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#unitOfWork = dependencies.unitOfWork;
    this.#restaurantOrders = dependencies.restaurantOrders;
    this.#restaurants = dependencies.restaurants;
    this.#menus = dependencies.menus;
    this.#conditions = dependencies.conditions;
    this.#readyTimeEstimator = dependencies.readyTimeEstimator;
    this.#outbox = dependencies.outbox;
    this.#clock = dependencies.clock;
  }

  async execute(command: AcceptOrder): Promise<RestaurantOrderView> {
    await authorize(
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
      const restaurant = await this.#restaurants.get(order.restaurantId);
      if (restaurant === undefined) {
        throw new NotFoundError("Restaurant", order.restaurantId);
      }
      const menu = await this.#menus.findByRestaurantId(order.restaurantId);
      const conditions = await this.#conditions.load({
        restaurantId: order.restaurantId,
        menuItemIds: [...new Set(order.items.map((item) => item.menuItemId))],
        now,
      });
      const eligibility = evaluateOrderAcceptance({
        now,
        restaurant,
        menu,
        items: order.items.map((item) => ({
          menuItemId: item.menuItemId,
          quantity: item.quantity.value,
        })),
        conditions,
      });
      const expectedReadyAt = await this.#readyTimeEstimator.estimate({
        restaurantId: order.restaurantId,
        items: order.items,
        acceptedAt: now,
      });
      order.accept({ acceptedAt: now, expectedReadyAt, eligibility });
      order.queue({ queuedAt: now });
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
