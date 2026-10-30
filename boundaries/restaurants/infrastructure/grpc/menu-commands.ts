import { create } from "@bufbuild/protobuf";
import {
  Code,
  ConnectError,
  type HandlerContext,
  type ServiceImpl,
} from "@connectrpc/connect";
import type { Subject } from "../../pkg/authz/subject.ts";
import type { MenuItemChangeResult } from "../../slices/menu/applications/menu-support/menu-support.ts";
import { requireField, transportError } from "./errors.ts";
import {
  AddMenuItemResponseSchema,
  CreateMenuResponseSchema,
  type RestaurantsService,
  SetMenuItemAvailabilityResponseSchema,
  UpdateMenuItemResponseSchema,
} from "./gen/nomnom/restaurants/v1/service_pb.js";
import { toMenuItemMessage } from "./messages.ts";
import { withRequestRevision } from "./revision.ts";
import type { RestaurantsServiceDependencies } from "./service.ts";
import type { SubjectResolver } from "./subject.ts";

type MenuRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  "createMenu" | "addMenuItem" | "updateMenuItem" | "setMenuItemAvailability"
>;

const toMenuItemChange = (result: MenuItemChangeResult) => ({
  menuId: result.menuId,
  menuItem: toMenuItemMessage(result),
  version: BigInt(result.version),
});

/**
 * Menu management commands (FR-210..FR-215) over `restaurant#manage_menu`.
 * Validation of names and prices stays in the domain (`invalid_menu`,
 * `invalid_money` → INVALID_ARGUMENT); only identifiers and the required
 * price are checked here.
 */
export const createMenuRpcs = (
  dependencies: RestaurantsServiceDependencies,
  subjects: SubjectResolver,
): MenuRpcs => {
  const caller = async (
    restaurantId: string,
    context: HandlerContext,
  ): Promise<Subject> => {
    const subject = withRequestRevision(
      await subjects.resolve(context),
      context,
    );
    requireField("restaurant_id", restaurantId);
    return subject;
  };
  return {
    async createMenu(request, context) {
      try {
        const subject = await caller(request.restaurantId, context);
        const result = await dependencies.createMenu.execute({
          subject,
          restaurantId: request.restaurantId,
        });
        return create(CreateMenuResponseSchema, {
          menuId: result.menuId,
          restaurantId: result.restaurantId,
          version: BigInt(result.version),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async addMenuItem(request, context) {
      try {
        const subject = await caller(request.restaurantId, context);
        if (request.price === undefined) {
          throw new ConnectError("price is required", Code.InvalidArgument);
        }
        const result = await dependencies.addMenuItem.execute({
          subject,
          restaurantId: request.restaurantId,
          name: request.name,
          description:
            request.description === "" ? undefined : request.description,
          price: {
            amount: request.price.amount,
            currency: request.price.currency,
          },
          isAvailable: request.available,
        });
        return create(AddMenuItemResponseSchema, toMenuItemChange(result));
      } catch (error) {
        throw transportError(error);
      }
    },

    async updateMenuItem(request, context) {
      try {
        const subject = await caller(request.restaurantId, context);
        requireField("menu_item_id", request.menuItemId);
        const result = await dependencies.updateMenuItem.execute({
          subject,
          restaurantId: request.restaurantId,
          menuItemId: request.menuItemId,
          name: request.name,
          // An empty description removes it (domain normalization).
          description: request.description,
          price:
            request.price === undefined
              ? undefined
              : {
                  amount: request.price.amount,
                  currency: request.price.currency,
                },
          isAvailable: request.available,
        });
        return create(UpdateMenuItemResponseSchema, {
          ...toMenuItemChange(result),
          changed: result.changed,
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async setMenuItemAvailability(request, context) {
      try {
        const subject = await caller(request.restaurantId, context);
        requireField("menu_item_id", request.menuItemId);
        const result = await dependencies.setMenuItemAvailability.execute({
          subject,
          restaurantId: request.restaurantId,
          menuItemId: request.menuItemId,
          isAvailable: request.available,
        });
        return create(SetMenuItemAvailabilityResponseSchema, {
          ...toMenuItemChange(result),
          changed: result.changed,
        });
      } catch (error) {
        throw transportError(error);
      }
    },
  };
};
