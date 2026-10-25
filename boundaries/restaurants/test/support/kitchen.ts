import type { Outbox } from "../../pkg/application/outbox.ts";
import type { Subject } from "../../pkg/application/subject.ts";
import type { UnitOfWork } from "../../pkg/application/unit-of-work.ts";
import { SequentialIdGenerator } from "../../pkg/infrastructure/memory/id-generator.ts";
import { AcceptOrderHandler } from "../../slices/restaurantorder/applications/accept-order/accept-order.ts";
import { CancelRestaurantOrderHandler } from "../../slices/restaurantorder/applications/cancel-restaurant-order/cancel-restaurant-order.ts";
import { GetKitchenQueueHandler } from "../../slices/restaurantorder/applications/get-kitchen-queue/get-kitchen-queue.ts";
import { GetRestaurantOrderHandler } from "../../slices/restaurantorder/applications/get-restaurant-order/get-restaurant-order.ts";
import type { RepeatPolicy } from "../../slices/restaurantorder/applications/kitchen-command.ts";
import { ListActivePreparationsHandler } from "../../slices/restaurantorder/applications/list-active-preparations/list-active-preparations.ts";
import { ListIncomingOrdersHandler } from "../../slices/restaurantorder/applications/list-incoming-orders/list-incoming-orders.ts";
import { ListReadyOrdersHandler } from "../../slices/restaurantorder/applications/list-ready-orders/list-ready-orders.ts";
import { MarkAssembledHandler } from "../../slices/restaurantorder/applications/mark-assembled/mark-assembled.ts";
import { MarkReadyForPickupHandler } from "../../slices/restaurantorder/applications/mark-ready-for-pickup/mark-ready-for-pickup.ts";
import { QueueRestaurantOrderHandler } from "../../slices/restaurantorder/applications/queue-restaurant-order/queue-restaurant-order.ts";
import { ReceiveOrderRequestHandler } from "../../slices/restaurantorder/applications/receive-order-request/receive-order-request.ts";
import { RejectOrderHandler } from "../../slices/restaurantorder/applications/reject-order/reject-order.ts";
import { ReportPreparationDelayHandler } from "../../slices/restaurantorder/applications/report-preparation-delay/report-preparation-delay.ts";
import { ReportPreparationFailureHandler } from "../../slices/restaurantorder/applications/report-preparation-failure/report-preparation-failure.ts";
import { StartPreparationHandler } from "../../slices/restaurantorder/applications/start-preparation/start-preparation.ts";
import type { CancellationPolicy } from "../../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import { FifoKitchenQueueOrdering } from "../../slices/restaurantorder/infrastructure/kitchen-queue/mvp.ts";
import {
  createRestaurantOrderStore,
  FixedDurationReadyTimeEstimator,
  InMemoryKitchenAccessControl,
  InMemoryKitchenBoard,
  InMemoryRestaurantOrderRepository,
  StaticAcceptanceConditions,
} from "../../slices/restaurantorder/infrastructure/memory/restaurant-orders.ts";
import {
  RepositoryAcceptanceFactsReader,
  RepositoryRestaurantExistence,
} from "../../slices/restaurantorder/infrastructure/restaurant-facts/restaurant-facts.ts";
import { createMenuWorld } from "./menus.ts";
import { createInfrastructure, type Infrastructure } from "./restaurants.ts";

export const cook: Subject = { type: "user", id: "cook" };
export const ordersService: Subject = { type: "service", id: "orders-worker" };
export const PREPARATION_MINUTES = 20;
export const minutes = (value: number): number => value * 60_000;

export const createKitchenWorld = (
  options: {
    infrastructure?: Infrastructure;
    outbox?: Outbox;
    /** Unit of Work of the commands; defaults to the in-memory one. */
    unitOfWork?: UnitOfWork;
    onRepeat?: RepeatPolicy;
    cancellationDuringPreparation?: CancellationPolicy["duringPreparation"];
  } = {},
) => {
  const infrastructure = options.infrastructure ?? createInfrastructure();
  const menuWorld = createMenuWorld(infrastructure);
  const store = createRestaurantOrderStore();
  infrastructure.unitOfWork.enlist(store);
  const restaurantOrders = new InMemoryRestaurantOrderRepository(store);
  const board = new InMemoryKitchenBoard(store);
  const accessControl = new InMemoryKitchenAccessControl();
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
    listIncomingOrders: new ListIncomingOrdersHandler({
      accessControl,
      reader: board,
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
