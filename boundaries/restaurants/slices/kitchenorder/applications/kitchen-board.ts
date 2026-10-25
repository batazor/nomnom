import type { Subject } from "../../../pkg/application/subject.ts";
import type { KitchenAccessControl } from "./access-control.ts";
import { authorize } from "./restaurant-order-support.ts";

export type KitchenBoardItem = Readonly<{
  menuItemId: string;
  quantity: number;
  options: readonly string[];
}>;

export type KitchenBoardQuery = Readonly<{
  subject: Subject;
  restaurantId: string;
}>;

/** Kitchen board read models are visible to the restaurant staff only. */
export const authorizeKitchenBoard = (
  accessControl: KitchenAccessControl,
  query: KitchenBoardQuery,
  action: string,
): Promise<void> =>
  authorize(
    accessControl.canOperateKitchen(query.subject, query.restaurantId),
    action,
  );
