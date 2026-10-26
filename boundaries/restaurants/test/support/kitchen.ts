import type { Outbox } from "../../pkg/application/outbox.ts";
import type { Subject } from "../../pkg/application/subject.ts";
import type { UnitOfWork } from "../../pkg/application/unit-of-work.ts";
import {
  type AcceptanceConditionsProvider,
  AcceptOrderHandler,
  type ReadyTimeEstimator,
} from "../../slices/restaurantorder/applications/accept-order/accept-order.ts";
import type {
  FulfillmentCoordinatorAccess,
  KitchenStaffAccess,
  RestaurantOrderViewAccess,
} from "../../slices/restaurantorder/applications/access-control.ts";
import { CancelRestaurantOrderHandler } from "../../slices/restaurantorder/applications/cancel-restaurant-order/cancel-restaurant-order.ts";
import { GetKitchenQueueHandler } from "../../slices/restaurantorder/applications/get-kitchen-queue/get-kitchen-queue.ts";
import { GetRestaurantOrderHandler } from "../../slices/restaurantorder/applications/get-restaurant-order/get-restaurant-order.ts";
import type { RepeatPolicy } from "../../slices/restaurantorder/applications/kitchen-command.ts";
import { ListActivePreparationsHandler } from "../../slices/restaurantorder/applications/list-active-preparations/list-active-preparations.ts";
import { ListReadyOrdersHandler } from "../../slices/restaurantorder/applications/list-ready-orders/list-ready-orders.ts";
import { MarkAssembledHandler } from "../../slices/restaurantorder/applications/mark-assembled/mark-assembled.ts";
import { MarkReadyForPickupHandler } from "../../slices/restaurantorder/applications/mark-ready-for-pickup/mark-ready-for-pickup.ts";
import { QueueRestaurantOrderHandler } from "../../slices/restaurantorder/applications/queue-restaurant-order/queue-restaurant-order.ts";
import { ReceiveOrderRequestHandler } from "../../slices/restaurantorder/applications/receive-order-request/receive-order-request.ts";
import { RejectOrderHandler } from "../../slices/restaurantorder/applications/reject-order/reject-order.ts";
import { ReportPreparationDelayHandler } from "../../slices/restaurantorder/applications/report-preparation-delay/report-preparation-delay.ts";
import { ReportPreparationFailureHandler } from "../../slices/restaurantorder/applications/report-preparation-failure/report-preparation-failure.ts";
import { StartPreparationHandler } from "../../slices/restaurantorder/applications/start-preparation/start-preparation.ts";
import type { AcceptanceConditions } from "../../slices/restaurantorder/domains/restaurantorder/acceptance.ts";
import type { CancellationPolicy } from "../../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import { FifoKitchenQueueOrdering } from "../../slices/restaurantorder/infrastructure/kitchen-queue/mvp.ts";
import {
  createRestaurantOrderCollection,
  DatabaseKitchenBoard,
  DatabaseRestaurantOrderRepository,
} from "../../slices/restaurantorder/infrastructure/repository/restaurant-orders.ts";
import {
  RepositoryAcceptanceFactsReader,
  RepositoryRestaurantExistence,
} from "../../slices/restaurantorder/infrastructure/restaurant-facts/restaurant-facts.ts";
import { noKitchenChanges } from "./database.ts";
import { createMenuWorld } from "./menus.ts";
import {
  createInfrastructure,
  type Infrastructure,
  SequentialIdGenerator,
} from "./restaurants.ts";

export const cook: Subject = { type: "user", id: "cook" };
export const ordersService: Subject = { type: "service", id: "orders-worker" };
export const PREPARATION_MINUTES = 20;
export const minutes = (value: number): number => value * 60_000;

export const createKitchenWorld = async (
  options: {
    infrastructure?: Infrastructure;
    outbox?: Outbox;
    /** Unit of Work of the commands; defaults to the MongoDB one. */
    unitOfWork?: UnitOfWork;
    onRepeat?: RepeatPolicy;
    cancellationDuringPreparation?: CancellationPolicy["duringPreparation"];
  } = {},
) => {
  const infrastructure =
    options.infrastructure ?? (await createInfrastructure());
  const menuWorld = await createMenuWorld(infrastructure);
  const store = createRestaurantOrderCollection(
    infrastructure.db,
    infrastructure.unitOfWork,
  );
  const restaurantOrders = new DatabaseRestaurantOrderRepository(
    store,
    noKitchenChanges,
  );
  const board = new DatabaseKitchenBoard(store);
  const accessControl = new TestKitchenAccessControl();
  const conditions = new StaticAcceptanceConditions();
  const outbox = options.outbox ?? infrastructure.outbox;
  const ids = new SequentialIdGenerator("restaurant-order");
  const commandDependencies = {
    accessControl,
    unitOfWork: options.unitOfWork ?? infrastructure.unitOfWork,
    restaurantOrders,
    outbox,
    clock: infrastructure.clock,
  };
  const onRepeat = options.onRepeat ?? "succeed-without-event";

  const handlers = {
    receive: new ReceiveOrderRequestHandler({
      accessControl,
      restaurants: new RepositoryRestaurantExistence(menuWorld.restaurants),
      restaurantOrders,
      ids,
      clock: infrastructure.clock,
    }),
    accept: new AcceptOrderHandler({
      ...commandDependencies,
      facts: new RepositoryAcceptanceFactsReader(menuWorld),
      conditions,
      readyTimeEstimator: new FixedDurationReadyTimeEstimator(
        minutes(PREPARATION_MINUTES),
      ),
    }),
    reject: new RejectOrderHandler(commandDependencies),
    queue: new QueueRestaurantOrderHandler({
      restaurantOrders,
      clock: infrastructure.clock,
    }),
    startPreparation: new StartPreparationHandler({
      ...commandDependencies,
      onRepeat,
    }),
    markAssembled: new MarkAssembledHandler({
      accessControl,
      restaurantOrders,
      clock: infrastructure.clock,
    }),
    markReady: new MarkReadyForPickupHandler({
      ...commandDependencies,
      onRepeat,
    }),
    reportDelay: new ReportPreparationDelayHandler(commandDependencies),
    reportFailure: new ReportPreparationFailureHandler(commandDependencies),
    cancel: new CancelRestaurantOrderHandler({
      ...commandDependencies,
      cancellationDuringPreparation:
        options.cancellationDuringPreparation ?? "reject",
    }),
    getRestaurantOrder: new GetRestaurantOrderHandler({
      accessControl,
      reader: board,
    }),
    getKitchenQueue: new GetKitchenQueueHandler({
      accessControl,
      reader: board,
      ordering: new FifoKitchenQueueOrdering(),
    }),
    listActivePreparations: new ListActivePreparationsHandler({
      accessControl,
      reader: board,
    }),
    listReadyOrders: new ListReadyOrdersHandler({
      accessControl,
      reader: board,
    }),
  };

  /** Open restaurant with an available "Pho" item, staff and orders access. */
  const givenKitchen = async (
    name = "Pho Bar",
  ): Promise<{ restaurantId: string; phoId: string }> => {
    const restaurantId = await menuWorld.givenRestaurantWithMenu(name);
    const phoId = await menuWorld.givenMenuItem(restaurantId, "Pho");
    accessControl.grantStaff(cook, restaurantId);
    accessControl.grantFulfillment(ordersService, restaurantId);
    return { restaurantId, phoId };
  };

  let orderSequence = 0;
  const givenReceivedOrder = async (
    restaurantId: string,
    menuItemId: string,
  ) => {
    orderSequence += 1;
    const { restaurantOrder } = await handlers.receive.execute({
      subject: ordersService,
      restaurantId,
      orderId: `order-${orderSequence}`,
      items: [{ menuItemId, quantity: 2 }],
    });
    return restaurantOrder;
  };

  type Stage = "RECEIVED" | "QUEUED" | "PREPARING" | "ASSEMBLED" | "READY";
  const givenOrderIn = async (
    stage: Stage,
    restaurantId: string,
    menuItemId: string,
  ) => {
    const received = await givenReceivedOrder(restaurantId, menuItemId);
    const command = {
      subject: cook,
      restaurantId,
      restaurantOrderId: received.restaurantOrderId,
    };
    const steps: Array<() => Promise<unknown>> = [
      () => handlers.accept.execute(command),
      () => handlers.startPreparation.execute(command),
      () => handlers.markAssembled.execute(command),
      () => handlers.markReady.execute(command),
    ];
    const count = [
      "RECEIVED",
      "QUEUED",
      "PREPARING",
      "ASSEMBLED",
      "READY",
    ].indexOf(stage);
    for (const step of steps.slice(0, count)) {
      await step();
    }
    return { ...command, orderId: received.orderId };
  };

  return {
    ...menuWorld,
    menuWorld,
    orderStore: store,
    restaurantOrders,
    board,
    kitchenAccessControl: accessControl,
    conditions,
    ...handlers,
    givenKitchen,
    givenReceivedOrder,
    givenOrderIn,
  };
};

/**
 * Test conditions: an always-open schedule with spare capacity unless the
 * test overrides the restaurant snapshot or a Menu Item offer.
 */
export class StaticAcceptanceConditions
  implements AcceptanceConditionsProvider
{
  restaurant: AcceptanceConditions["restaurant"] = {
    opensAt: new Date(0),
    closesAt: new Date(8.64e15),
    currentOrders: 0,
    maximumOrders: Number.MAX_SAFE_INTEGER,
    scheduleException: false,
  };
  readonly offers = new Map<
    string,
    AcceptanceConditions["menuItems"][string]
  >();

  async load(input: {
    restaurantId: string;
    menuItemIds: readonly string[];
    now: Date;
  }): Promise<AcceptanceConditions> {
    const menuItems: Record<string, AcceptanceConditions["menuItems"][string]> =
      {};
    for (const menuItemId of input.menuItemIds) {
      menuItems[menuItemId] = this.offers.get(menuItemId) ?? {
        availableFrom: new Date(0),
        availableUntil: new Date(8.64e15),
        remainingCapacity: Number.MAX_SAFE_INTEGER,
      };
    }
    return { restaurant: this.restaurant, menuItems };
  }
}

/** Test estimator: a fixed preparation duration after Acceptance. */
export class FixedDurationReadyTimeEstimator implements ReadyTimeEstimator {
  readonly #milliseconds: number;

  constructor(milliseconds: number) {
    this.#milliseconds = milliseconds;
  }

  async estimate(input: { acceptedAt: Date }): Promise<Date> {
    return new Date(input.acceptedAt.getTime() + this.#milliseconds);
  }
}

/** Test authorization graph: restaurant staff and fulfillment services. */
export class TestKitchenAccessControl
  implements
    KitchenStaffAccess,
    FulfillmentCoordinatorAccess,
    RestaurantOrderViewAccess
{
  readonly #staff = new Set<string>();
  readonly #fulfillment = new Set<string>();

  grantStaff(subject: Subject, restaurantId: string): this {
    this.#staff.add(`${subject.type}:${subject.id}@${restaurantId}`);
    return this;
  }

  grantFulfillment(subject: Subject, restaurantId: string): this {
    this.#fulfillment.add(`${subject.type}:${subject.id}@${restaurantId}`);
    return this;
  }

  async canOperateKitchen(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean> {
    return this.#staff.has(`${subject.type}:${subject.id}@${restaurantId}`);
  }

  async canCoordinateFulfillment(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean> {
    return (
      subject.type === "service" &&
      this.#fulfillment.has(`${subject.type}:${subject.id}@${restaurantId}`)
    );
  }

  async canViewRestaurantOrders(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean> {
    return (
      (await this.canOperateKitchen(subject, restaurantId)) ||
      (await this.canCoordinateFulfillment(subject, restaurantId))
    );
  }
}
