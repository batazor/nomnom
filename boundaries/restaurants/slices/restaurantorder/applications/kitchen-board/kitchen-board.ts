import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { KitchenStaffAccess } from "../access-control/access-control.ts";

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
