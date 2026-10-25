import type { Outbox } from "../../pkg/application/outbox.ts";
import type { Subject } from "../../pkg/application/subject.ts";
import { SequentialIdGenerator } from "../../pkg/infrastructure/memory/id-generator.ts";
import { AcceptOrderHandler } from "../../slices/kitchenorder/applications/accept-order/accept-order.ts";
import { CancelRestaurantOrderHandler } from "../../slices/kitchenorder/applications/cancel-restaurant-order/cancel-restaurant-order.ts";
import { GetKitchenQueueHandler } from "../../slices/kitchenorder/applications/get-kitchen-queue/get-kitchen-queue.ts";
import { GetRestaurantOrderHandler } from "../../slices/kitchenorder/applications/get-restaurant-order/get-restaurant-order.ts";
import { ListActivePreparationsHandler } from "../../slices/kitchenorder/applications/list-active-preparations/list-active-preparations.ts";
import { ListReadyOrdersHandler } from "../../slices/kitchenorder/applications/list-ready-orders/list-ready-orders.ts";
import { MarkAssembledHandler } from "../../slices/kitchenorder/applications/mark-assembled/mark-assembled.ts";
import { MarkReadyForPickupHandler } from "../../slices/kitchenorder/applications/mark-order-ready/mark-order-ready.ts";
import { QueueRestaurantOrderHandler } from "../../slices/kitchenorder/applications/queue-restaurant-order/queue-restaurant-order.ts";
import { ReceiveOrderRequestHandler } from "../../slices/kitchenorder/applications/receive-order-request/receive-order-request.ts";
import { RejectOrderHandler } from "../../slices/kitchenorder/applications/reject-order/reject-order.ts";
import { ReportPreparationDelayHandler } from "../../slices/kitchenorder/applications/report-preparation-delay/report-preparation-delay.ts";
import { ReportPreparationFailureHandler } from "../../slices/kitchenorder/applications/report-preparation-failure/report-preparation-failure.ts";
import type { RepeatPolicy } from "../../slices/kitchenorder/applications/restaurant-order-support.ts";
import { StartPreparationHandler } from "../../slices/kitchenorder/applications/start-preparing-order/start-preparing-order.ts";
import type { CancellationPolicy } from "../../slices/kitchenorder/domains/restaurantorder/restaurantorder.ts";
import {
  createRestaurantOrderStore,
  FifoKitchenQueueOrdering,
  FixedDurationReadyTimeEstimator,
  InMemoryKitchenAccessControl,
  InMemoryKitchenBoard,
  InMemoryRestaurantOrderRepository,
  StaticAcceptanceConditions,
} from "../../slices/kitchenorder/infrastructure/memory/restaurant-orders.ts";
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
    unitOfWork: infrastructure.unitOfWork,
    restaurantOrders,
    outbox,
    clock: infrastructure.clock,
  };
  const onRepeat = options.onRepeat ?? "succeed-without-event";

  const handlers = {
    receive: new ReceiveOrderRequestHandler({
      accessControl,
      restaurants: menuWorld.restaurants,
      restaurantOrders,
      ids,
      clock: infrastructure.clock,
    }),
    accept: new AcceptOrderHandler({
      ...commandDependencies,
      restaurants: menuWorld.restaurants,
      menus: menuWorld.menus,
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
