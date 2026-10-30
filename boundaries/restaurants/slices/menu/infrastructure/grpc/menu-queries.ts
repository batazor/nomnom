import { create } from "@bufbuild/protobuf";
import type { ServiceImpl } from "@connectrpc/connect";
import {
  requireField,
  transportError,
} from "../../../../infrastructure/grpc/errors.ts";
import {
  BatchGetMenusResponseSchema,
  GetMenuResponseSchema,
  type RestaurantsService,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import type { BatchGetMenusHandler } from "../../applications/batch-get-menus/batch-get-menus.ts";
import type { GetMenuHandler } from "../../applications/get-menu/get-menu.ts";
import { toMenuItemMessages } from "./messages.ts";

export type MenuQueryRpcDependencies = Readonly<{
  batchGetMenus: Pick<BatchGetMenusHandler, "execute">;
  getMenu: Pick<GetMenuHandler, "execute">;
}>;

type MenuQueryRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  "getMenu" | "batchGetMenus"
>;

export const createMenuQueryRpcs = (
  dependencies: MenuQueryRpcDependencies,
): MenuQueryRpcs => ({
  async getMenu(request) {
    try {
      requireField("restaurant_id", request.restaurantId);
      const view = await dependencies.getMenu.execute({
        restaurantId: request.restaurantId,
      });
      return create(GetMenuResponseSchema, {
        items: toMenuItemMessages(view),
      });
    } catch (error) {
      throw transportError(error);
    }
  },

  async batchGetMenus(request) {
    try {
      const views = await dependencies.batchGetMenus.execute({
        restaurantIds: request.restaurantIds,
      });
      return create(BatchGetMenusResponseSchema, {
        menus: views.map((view) => ({
          restaurantId: view.restaurantId,
          items: toMenuItemMessages(view),
        })),
      });
    } catch (error) {
      throw transportError(error);
    }
  },
});
