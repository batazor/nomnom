import { create } from "@bufbuild/protobuf";
import { MenuItemSchema } from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/restaurants_pb.js";
import type {
  MenuItemView,
  MenuView,
} from "../../applications/menu-support/menu-support.ts";

export const toMenuItemMessage = (item: MenuItemView) =>
  create(MenuItemSchema, {
    menuItemId: item.menuItemId,
    name: item.name,
    description: item.description ?? "",
    price: { amount: item.price.amount, currency: item.price.currency },
    available: item.isAvailable,
  });

export const toMenuItemMessages = (view: MenuView) =>
  view.items.map(toMenuItemMessage);
