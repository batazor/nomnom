import type { Clock } from "../../../pkg/application/clock.ts";
import {
  NotFoundError,
  PermissionDeniedError,
} from "../../../pkg/application/errors.ts";
import type { Outbox } from "../../../pkg/application/outbox.ts";
import type { Subject } from "../../../pkg/application/subject.ts";
import type { UnitOfWork } from "../../../pkg/application/unit-of-work.ts";
import {
  AlreadyInStateError,
  type RestaurantOrder,
  type RestaurantOrderEventType,
  type RestaurantOrderRepository,
  type RestaurantOrderSnapshot,
  type RestaurantOrderState,
  type RestaurantOrderTimeline,
} from "../domains/restaurantorder/restaurantorder.ts";
import type { KitchenAccessControl } from "./access-control.ts";

/** Representation of one Restaurant Order returned by commands and queries. */
export type RestaurantOrderView = Readonly<{
  restaurantOrderId: string;
  orderId: string;
  restaurantId: string;
  state: RestaurantOrderState;
  items: readonly Readonly<{
    menuItemId: string;
    quantity: number;
    options: readonly string[];
  }>[];
  timeline: RestaurantOrderTimeline;
  rejectionReason: string | undefined;
  failureReason: string | undefined;
  cancelledAtStage: RestaurantOrderState | undefined;
  version: number;
}>;

export const snapshotToView = (
  snapshot: RestaurantOrderSnapshot,
  version: number,
): RestaurantOrderView => ({
  restaurantOrderId: snapshot.id,
  orderId: snapshot.orderId,
  restaurantId: snapshot.restaurantId,
  state: snapshot.state,
  items: snapshot.items.map((item) => ({
    menuItemId: item.menuItemId,
    quantity: item.quantity,
    options: [...item.options],
  })),
  timeline: { ...snapshot.timeline },
  rejectionReason: snapshot.rejectionReason,
  failureReason: snapshot.failureReason,
  cancelledAtStage: snapshot.cancellation?.stage,
  version,
});

export const toRestaurantOrderView = (
  order: RestaurantOrder,
  version: number = order.version,
): RestaurantOrderView => snapshotToView(order.toSnapshot(), version);

/**
 * Policy for repeating Start Preparation or Mark Ready for Pickup on an order
 * already in the target state. It is an open product decision, so it has no
 * default and is chosen in the composition root.
 */
export type RepeatPolicy = "succeed-without-event" | "reject";

export const authorize = async (
  allowed: Promise<boolean>,
  action: string,
): Promise<void> => {
  if (!(await allowed)) {
    throw new PermissionDeniedError(action);
  }
};

/**
 * Loads an order addressed by restaurant and id. An order of another
 * restaurant is reported as not found so its state is not disclosed.
 */
export const loadRestaurantOrder = async (
  orders: RestaurantOrderRepository,
  restaurantId: string,
  restaurantOrderId: string,
): Promise<RestaurantOrder> => {
  const order = await orders.get(restaurantOrderId);
  if (order === undefined || order.restaurantId !== restaurantId) {
    throw new NotFoundError("RestaurantOrder", restaurantOrderId);
  }
  return order;
};

/**
 * Saves the order and passes only the facts of `published` to the Outbox;
 * local facts are discarded. Must run inside the active Unit of Work.
 */
export const saveAndPublish = async (
  orders: RestaurantOrderRepository,
  outbox: Outbox,
  order: RestaurantOrder,
  published: readonly RestaurantOrderEventType[],
): Promise<number> => {
  const version = await orders.save(order);
  const events = order
    .pullEvents()
    .filter((event) => published.includes(event.type));
  if (events.length > 0) {
    await outbox.add(
      { type: "RestaurantOrder", id: order.id, version },
      events,
    );
  }
  return version;
};

export type KitchenCommand = Readonly<{
  subject: Subject;
  restaurantId: string;
  restaurantOrderId: string;
}>;

export type KitchenCommandDependencies = {
  accessControl: KitchenAccessControl;
  unitOfWork: UnitOfWork;
  restaurantOrders: RestaurantOrderRepository;
  outbox: Outbox;
  clock: Clock;
};

/**
 * Shared flow of kitchen staff commands that publish a fact:
 * authorize → Unit of Work { load → domain method → save → Outbox } → view.
 */
export const executeKitchenCommand = async (
  dependencies: KitchenCommandDependencies,
  command: KitchenCommand,
  options: Readonly<{
    action: string;
    published: readonly RestaurantOrderEventType[];
    change: (order: RestaurantOrder, now: Date) => void;
    repeatPolicy?: RepeatPolicy;
  }>,
): Promise<RestaurantOrderView> => {
  await authorize(
    dependencies.accessControl.canOperateKitchen(
      command.subject,
      command.restaurantId,
    ),
    options.action,
  );
  const now = dependencies.clock.now();
  return dependencies.unitOfWork.run(async () => {
    const order = await loadRestaurantOrder(
      dependencies.restaurantOrders,
      command.restaurantId,
      command.restaurantOrderId,
    );
    try {
      options.change(order, now);
    } catch (error) {
      if (
        error instanceof AlreadyInStateError &&
        options.repeatPolicy === "succeed-without-event"
      ) {
        return toRestaurantOrderView(order);
      }
      throw error;
    }
    const version = await saveAndPublish(
      dependencies.restaurantOrders,
      dependencies.outbox,
      order,
      options.published,
    );
    return toRestaurantOrderView(order, version);
  });
};
