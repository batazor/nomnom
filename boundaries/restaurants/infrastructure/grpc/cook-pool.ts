import { create } from "@bufbuild/protobuf";
import type { HandlerContext, ServiceImpl } from "@connectrpc/connect";
import type { CookCommand } from "../../slices/cook/applications/change-cook.ts";
import type { CookView } from "../../slices/cook/applications/cook-view.ts";
import { requireField, transportError } from "./errors.ts";
import {
  CookSchema,
  GetCookPoolResponseSchema,
  HireCookResponseSchema,
  PutCookOnLineResponseSchema,
  type RestaurantsService,
  RetireCookResponseSchema,
  TakeCookOffLineResponseSchema,
} from "./gen/nomnom/restaurants/v1/service_pb.js";
import { withRequestRevision } from "./revision.ts";
import type { RestaurantsServiceDependencies } from "./service.ts";
import type { SubjectResolver } from "./subject.ts";

type CookPoolRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  | "hireCook"
  | "putCookOnLine"
  | "takeCookOffLine"
  | "retireCook"
  | "getCookPool"
>;

const toCookMessage = (view: CookView) =>
  create(CookSchema, {
    cookId: view.cookId,
    restaurantId: view.restaurantId,
    name: view.name,
    onLine: view.onLine,
    currentRestaurantOrderId: view.currentRestaurantOrderId ?? "",
  });

/** Cook Pool RPCs of restaurant staff (FR-614..FR-616). */
export const createCookPoolRpcs = (
  dependencies: RestaurantsServiceDependencies,
  subjects: SubjectResolver,
): CookPoolRpcs => {
  /** Resolves the caller and the addressed cook of a line command. */
  const cookCommand = async (
    request: { restaurantId: string; cookId: string },
    context: HandlerContext,
  ): Promise<CookCommand> => {
    const subject = withRequestRevision(
      await subjects.resolve(context),
      context,
    );
    requireField("restaurant_id", request.restaurantId);
    requireField("cook_id", request.cookId);
    return {
      subject,
      restaurantId: request.restaurantId,
      cookId: request.cookId,
    };
  };
  return {
    async hireCook(request, context) {
      try {
        const subject = withRequestRevision(
          await subjects.resolve(context),
          context,
        );
        requireField("restaurant_id", request.restaurantId);
        const view = await dependencies.hireCook.execute({
          subject,
          restaurantId: request.restaurantId,
          name: request.name,
        });
        return create(HireCookResponseSchema, { cook: toCookMessage(view) });
      } catch (error) {
        throw transportError(error);
      }
    },

    async putCookOnLine(request, context) {
      try {
        const result = await dependencies.putCookOnLine.execute(
          await cookCommand(request, context),
        );
        return create(PutCookOnLineResponseSchema, {
          cook: toCookMessage(result),
          changed: result.changed,
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async takeCookOffLine(request, context) {
      try {
        const result = await dependencies.takeCookOffLine.execute(
          await cookCommand(request, context),
        );
        return create(TakeCookOffLineResponseSchema, {
          cook: toCookMessage(result),
          changed: result.changed,
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async retireCook(request, context) {
      try {
        const result = await dependencies.retireCook.execute(
          await cookCommand(request, context),
        );
        return create(RetireCookResponseSchema, { changed: result.changed });
      } catch (error) {
        throw transportError(error);
      }
    },

    async getCookPool(request, context) {
      try {
        const subject = withRequestRevision(
          await subjects.resolve(context),
          context,
        );
        requireField("restaurant_id", request.restaurantId);
        const pool = await dependencies.getCookPool.execute({
          subject,
          restaurantId: request.restaurantId,
        });
        return create(GetCookPoolResponseSchema, {
          size: pool.size,
          onLine: pool.onLine,
          busy: pool.busy,
          queued: pool.queued,
          cooks: pool.cooks.map(toCookMessage),
        });
      } catch (error) {
        throw transportError(error);
      }
    },
  };
};
