import type { Subject } from "../../pkg/authz/subject.ts";
import type { Outbox } from "../../pkg/outbox/outbox.ts";
import type { UnitOfWork } from "../../pkg/unit-of-work/unit-of-work.ts";
import type { CookPoolAccess } from "../../slices/cook/applications/access-control/access-control.ts";
import { PoolCookAssigner } from "../../slices/cook/applications/assign/assign.ts";
import { GetCookPoolHandler } from "../../slices/cook/applications/get-pool/get-pool.ts";
import { HireCookHandler } from "../../slices/cook/applications/hire/hire.ts";
import { PutCookOnLineHandler } from "../../slices/cook/applications/put-on-line/put-on-line.ts";
import { RetireCookHandler } from "../../slices/cook/applications/retire/retire.ts";
import { TakeCookOffLineHandler } from "../../slices/cook/applications/take-off-line/take-off-line.ts";
import { DatabaseCookRepository } from "../../slices/cook/infrastructure/repository/cooks.ts";
import {
  createRestaurantCollection,
  DatabaseRestaurantWaitTimes,
} from "../../slices/restaurant/infrastructure/repository/restaurants.ts";
import {
  type AcceptanceConditionsProvider,
  AcceptOrderHandler,
  type ReadyTimeEstimator,
} from "../../slices/restaurantorder/applications/acceptance/accept/accept.ts";
import { ReceiveOrderRequestHandler } from "../../slices/restaurantorder/applications/acceptance/receive-request/receive-request.ts";
import { RejectOrderHandler } from "../../slices/restaurantorder/applications/acceptance/reject/reject.ts";
import type {
  FulfillmentCoordinatorAccess,
  FulfillmentProcessAccess,
  KitchenStaffAccess,
  RestaurantOrderViewAccess,
} from "../../slices/restaurantorder/applications/access-control/access-control.ts";
import { CancelRestaurantOrderHandler } from "../../slices/restaurantorder/applications/cancel/cancel.ts";
import { GetRestaurantOrderHandler } from "../../slices/restaurantorder/applications/get/get.ts";
import { GetKitchenQueueHandler } from "../../slices/restaurantorder/applications/kitchen-board/get-queue/get-queue.ts";
import { ListActivePreparationsHandler } from "../../slices/restaurantorder/applications/kitchen-board/list-active-preparations/list-active-preparations.ts";
import { ListReadyOrdersHandler } from "../../slices/restaurantorder/applications/kitchen-board/list-ready/list-ready.ts";
import { ListScheduledOrdersHandler } from "../../slices/restaurantorder/applications/kitchen-board/list-scheduled/list-scheduled.ts";
import type { RepeatPolicy } from "../../slices/restaurantorder/applications/kitchen-command/kitchen-command.ts";
import { MarkAssembledHandler } from "../../slices/restaurantorder/applications/preparation/mark-assembled/mark-assembled.ts";
import { MarkReadyForPickupHandler } from "../../slices/restaurantorder/applications/preparation/mark-ready-for-pickup/mark-ready-for-pickup.ts";
import { QueueRestaurantOrderHandler } from "../../slices/restaurantorder/applications/preparation/queue/queue.ts";
import { ReportPreparationDelayHandler } from "../../slices/restaurantorder/applications/preparation/report-delay/report-delay.ts";
import { ReportPreparationFailureHandler } from "../../slices/restaurantorder/applications/preparation/report-failure/report-failure.ts";
import { StartPreparationHandler } from "../../slices/restaurantorder/applications/preparation/start/start.ts";
import type { AcceptanceConditions } from "../../slices/restaurantorder/domains/restaurantorder/acceptance.ts";
import type { CancellationPolicy } from "../../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import { PriorityKitchenQueueOrdering } from "../../slices/restaurantorder/infrastructure/kitchen-queue/priority.ts";
import {
  createRestaurantOrderCollection,
  DatabaseKitchenBoard,
  DatabaseRestaurantOrderRepository,
} from "../../slices/restaurantorder/infrastructure/repository/restaurant-orders.ts";
import {
  RepositoryAcceptanceFactsReader,
  RepositoryRestaurantExistence,
} from "../../slices/restaurantorder/infrastructure/restaurant-facts/restaurant-facts.ts";
import type { SlotReservationAccess } from "../../slices/scheduleslot/applications/access-control/access-control.ts";
import { ConfirmScheduledSlotHandler } from "../../slices/scheduleslot/applications/confirm/confirm.ts";
import { ListScheduledSlotsHandler } from "../../slices/scheduleslot/applications/list/list.ts";
import { ReleaseScheduledSlotHandler } from "../../slices/scheduleslot/applications/release/release.ts";
import { ReserveScheduledSlotHandler } from "../../slices/scheduleslot/applications/reserve/reserve.ts";
import { DeliveryWindows } from "../../slices/scheduleslot/domains/scheduleslot/scheduleslot.ts";
import {
  createScheduleSlotCollection,
  DatabaseScheduleSlotRepository,
} from "../../slices/scheduleslot/infrastructure/repository/schedule-slots.ts";
import { noKitchenChanges } from "./database.ts";
import { createMenuWorld } from "./menus.ts";
import {
  createInfrastructure,
  type Infrastructure,
  SequentialIdGenerator,
  testWaitTimePolicy,
} from "./restaurants.ts";

export const cook: Subject = { type: "user", id: "cook" };
export const ordersService: Subject = { type: "service", id: "orders-worker" };
export const PREPARATION_MINUTES = 20;
export const minutes = (value: number): number => value * 60_000;
/** KITCHEN_QUEUE__AGING_FACTOR of the tests (the .env.example value). */
export const QUEUE_AGING_FACTOR = 2;
/** SCHEDULED_SLOT__* of the tests (the .env.example values). */
export const SLOT_WIDTH_MINUTES = 15;
export const SLOT_MIN_LEAD_MINUTES = 45;
export const SLOT_MAX_HORIZON_MINUTES = 48 * 60;
export const SLOT_HOLD_TTL_MINUTES = 10;
export const SLOT_ALTERNATIVES = 3;

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
  const cooks = new DatabaseCookRepository(
    infrastructure.db,
    infrastructure.unitOfWork,
    noKitchenChanges,
  );
  const accessControl = new TestKitchenAccessControl();
  // Kitchen Queue policy over the Wait Time Statistics of the restaurants;
  // without history the baseline is the preparation time of the tests.
  const ordering = new PriorityKitchenQueueOrdering({
    store: new DatabaseRestaurantWaitTimes(
      createRestaurantCollection(infrastructure.db, infrastructure.unitOfWork),
    ),
    policy: testWaitTimePolicy(minutes(PREPARATION_MINUTES)),
    agingFactor: QUEUE_AGING_FACTOR,
  });
  const startRequests = new SequentialIdGenerator("start-request");
  const scheduleSlots = new DatabaseScheduleSlotRepository(
    createScheduleSlotCollection(infrastructure.db, infrastructure.unitOfWork),
  );
  const deliveryWindows = new DeliveryWindows({
    widthMs: minutes(SLOT_WIDTH_MINUTES),
    minLeadMs: minutes(SLOT_MIN_LEAD_MINUTES),
    maxHorizonMs: minutes(SLOT_MAX_HORIZON_MINUTES),
  });
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
  const cookCommandDependencies = {
    accessControl,
    unitOfWork: options.unitOfWork ?? infrastructure.unitOfWork,
    cooks,
    workload: board,
    clock: infrastructure.clock,
  };

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
      accessControl,
      restaurantOrders,
      clock: infrastructure.clock,
    }),
    startPreparation: new StartPreparationHandler({
      ...commandDependencies,
      started: board,
      queue: board,
      ordering,
      cooks: new PoolCookAssigner({ cooks, workload: board }),
      onRepeat,
    }),
    hireCook: new HireCookHandler({
      accessControl,
      restaurants: new RepositoryRestaurantExistence(menuWorld.restaurants),
      cooks,
      ids: new SequentialIdGenerator("cook"),
    }),
    putCookOnLine: new PutCookOnLineHandler(cookCommandDependencies),
    takeCookOffLine: new TakeCookOffLineHandler(cookCommandDependencies),
    retireCook: new RetireCookHandler(cookCommandDependencies),
    getCookPool: new GetCookPoolHandler({
      accessControl,
      cooks,
      workload: board,
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
      ordering,
      clock: infrastructure.clock,
    }),
    listActivePreparations: new ListActivePreparationsHandler({
      accessControl,
      reader: board,
    }),
    listReadyOrders: new ListReadyOrdersHandler({
      accessControl,
      reader: board,
    }),
    listScheduledOrders: new ListScheduledOrdersHandler({
      accessControl,
      reader: board,
    }),
    listScheduledSlots: new ListScheduledSlotsHandler({
      restaurants: menuWorld.restaurants,
      slots: scheduleSlots,
      windows: deliveryWindows,
      clock: infrastructure.clock,
    }),
    reserveScheduledSlot: new ReserveScheduledSlotHandler({
      accessControl,
      unitOfWork: infrastructure.unitOfWork,
      restaurants: menuWorld.restaurants,
      slots: scheduleSlots,
      windows: deliveryWindows,
      holdTtlMs: minutes(SLOT_HOLD_TTL_MINUTES),
      alternatives: SLOT_ALTERNATIVES,
      clock: infrastructure.clock,
    }),
    confirmScheduledSlot: new ConfirmScheduledSlotHandler({
      accessControl,
      unitOfWork: infrastructure.unitOfWork,
      restaurants: menuWorld.restaurants,
      slots: scheduleSlots,
      clock: infrastructure.clock,
    }),
    releaseScheduledSlot: new ReleaseScheduledSlotHandler({
      accessControl,
      unitOfWork: infrastructure.unitOfWork,
      slots: scheduleSlots,
      clock: infrastructure.clock,
    }),
  };

  /** Hires cooks with the given names and puts them on line. */
  const givenCooksOnLine = async (
    restaurantId: string,
    names: readonly string[],
  ): Promise<string[]> => {
    const cookIds: string[] = [];
    for (const name of names) {
      const { cookId } = await handlers.hireCook.execute({
        subject: cook,
        restaurantId,
        name,
      });
      await handlers.putCookOnLine.execute({
        subject: cook,
        restaurantId,
        cookId,
      });
      cookIds.push(cookId);
    }
    return cookIds;
  };

  /**
   * Open restaurant with an available "Pho" item, staff and orders access and
   * `cooksOnLine` cooks on line (three unless the test needs another line).
   */
  const givenKitchen = async (
    name = "Pho Bar",
    cooksOnLine: readonly string[] = ["Aigul", "Oleg", "Marat"],
    /** Market of the restaurant; the launch Market of the tests by default. */
    marketId?: string,
  ): Promise<{ restaurantId: string; phoId: string; cookIds: string[] }> => {
    const restaurantId = await menuWorld.givenRestaurantWithMenu(
      name,
      marketId === undefined ? { open: true } : { open: true, marketId },
    );
    const phoId = await menuWorld.givenMenuItem(restaurantId, "Pho");
    accessControl.grantStaff(cook, restaurantId);
    accessControl.grantFulfillment(ordersService, restaurantId);
    const cookIds = await givenCooksOnLine(restaurantId, cooksOnLine);
    return { restaurantId, phoId, cookIds };
  };

  /** Start Next Preparation by the kitchen with a fresh request id. */
  const startNext = (
    restaurantId: string,
    requestId: string = startRequests.next(),
  ) =>
    handlers.startPreparation.execute({
      subject: cook,
      restaurantId,
      requestId,
    });

  let orderSequence = 0;
  const givenReceivedOrder = async (
    restaurantId: string,
    menuItemId: string,
    priority: "STANDARD" | "PREMIUM" = "STANDARD",
    schedule?: Readonly<{
      windowStart: Date;
      windowEnd: Date;
      plannedReadyAt: Date;
    }>,
  ) => {
    orderSequence += 1;
    const { restaurantOrder } = await handlers.receive.execute({
      subject: ordersService,
      restaurantId,
      orderId: `order-${orderSequence}`,
      items: [{ menuItemId, quantity: 2 }],
      priority,
      ...(schedule === undefined ? {} : { schedule }),
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
      // The Kitchen Queue chooses the order: the setup must make this one
      // its head, otherwise the test would silently start another order.
      async () => {
        const started = await startNext(restaurantId);
        if (started?.restaurantOrderId !== command.restaurantOrderId) {
          throw new Error(
            `queue head ${started?.restaurantOrderId} is not ${command.restaurantOrderId}`,
          );
        }
      },
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
    scheduleSlots,
    deliveryWindows,
    board,
    cooks,
    kitchenAccessControl: accessControl,
    conditions,
    ...handlers,
    givenKitchen,
    givenCooksOnLine,
    givenReceivedOrder,
    givenOrderIn,
    startNext,
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

/**
 * Test authorization graph: restaurant staff and fulfillment services. A
 * check without a restaurant (commands naming only an order) allows a
 * service granted fulfillment of any restaurant, like the platform check.
 */
export class TestKitchenAccessControl
  implements
    KitchenStaffAccess,
    FulfillmentCoordinatorAccess,
    FulfillmentProcessAccess,
    SlotReservationAccess,
    RestaurantOrderViewAccess,
    CookPoolAccess
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

  /** Staff manage the Cook Pool, like `restaurant#manage_cooks`. */
  async canManageCooks(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean> {
    return this.canOperateKitchen(subject, restaurantId);
  }

  async canCoordinateFulfillment(
    subject: Subject,
    restaurantId?: string,
  ): Promise<boolean> {
    const key = `${subject.type}:${subject.id}@`;
    return (
      subject.type === "service" &&
      (restaurantId === undefined
        ? [...this.#fulfillment].some((grant) => grant.startsWith(key))
        : this.#fulfillment.has(`${key}${restaurantId}`))
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
