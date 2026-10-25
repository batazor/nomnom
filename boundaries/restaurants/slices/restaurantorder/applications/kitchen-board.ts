import { requirePermission } from "../../../pkg/application/require-permission.ts";
import type { Subject } from "../../../pkg/application/subject.ts";
import type { KitchenStaffAccess } from "./access-control.ts";

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
  accessControl: KitchenStaffAccess,
  query: KitchenBoardQuery,
  action: string,
): Promise<void> =>
  requirePermission(
    accessControl.canOperateKitchen(query.subject, query.restaurantId),
    action,
  );
