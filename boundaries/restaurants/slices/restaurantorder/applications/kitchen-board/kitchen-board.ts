import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { RestaurantOrderState } from "../../domains/restaurantorder/restaurantorder.ts";
import type { KitchenStaffAccess } from "../access-control/access-control.ts";

/** Restaurant Order of any restaurant in one of the requested states. */
export type KitchenOrderInState<State extends RestaurantOrderState> = Readonly<{
  restaurantOrderId: string;
  restaurantId: string;
  marketId: string;
  state: State;
  expectedReadyAt?: Date;
  scheduled: boolean;
}>;

/** Read model over all restaurants; the caller names the states it needs. */
export interface KitchenOrdersInStatesReader {
  listKitchenOrdersInStates<State extends RestaurantOrderState>(
    states: readonly State[],
  ): Promise<readonly KitchenOrderInState<State>[]>;
}

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
