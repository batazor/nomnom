import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { Outbox } from "../../../../pkg/outbox/outbox.ts";
import type { UnitOfWork } from "../../../../pkg/unit-of-work/unit-of-work.ts";
import type { Clock } from "../../../../pkg/usecase/clock.ts";
import {
  AlreadyInStateError,
  type RestaurantOrder,
  type RestaurantOrderEventType,
  type RestaurantOrderRepository,
} from "../../domains/restaurantorder/restaurantorder.ts";
import type { KitchenStaffAccess } from "../access-control/access-control.ts";
import { loadRestaurantOrder } from "../load-restaurant-order/load-restaurant-order.ts";
import { saveAndPublish } from "../publish-order-change/publish-order-change.ts";
import {
  type RestaurantOrderView,
  toRestaurantOrderView,
} from "../restaurant-order-view/restaurant-order-view.ts";

/**
 * Policy for repeating Start Preparation or Mark Ready for Pickup on an order
 * already in the target state. It is an open product decision, so it has no
 * default and is chosen in the composition root.
 */
export type RepeatPolicy = "succeed-without-event" | "reject";

export type KitchenCommand = Readonly<{
  subject: Subject;
  restaurantId: string;
  restaurantOrderId: string;
}>;

export type KitchenCommandDependencies = {
  accessControl: KitchenStaffAccess;
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
    change: (order: RestaurantOrder, now: Date) => void | Promise<void>;
    repeatPolicy?: RepeatPolicy;
  }>,
): Promise<RestaurantOrderView> => {
  await requirePermission(
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
      await options.change(order, now);
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
