import { create } from "@bufbuild/protobuf";
import { durationMs } from "@bufbuild/protobuf/wkt";
import type { HandlerContext, ServiceImpl } from "@connectrpc/connect";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  requireField,
  transportError,
} from "../../../../infrastructure/grpc/errors.ts";
import type {
  Money,
  ScheduledOrderSettings,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/restaurants_pb.js";
import {
  CloseRestaurantResponseSchema,
  OpenRestaurantResponseSchema,
  type RestaurantsService,
  SetOpeningHoursResponseSchema,
  SetScheduledOrderSettingsResponseSchema,
  UpdateRestaurantResponseSchema,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { withRequestRevision } from "../../../../infrastructure/grpc/revision.ts";
import type { SubjectResolver } from "../../../../infrastructure/grpc/subject.ts";
import type { CloseRestaurantHandler } from "../../applications/close-restaurant/close-restaurant.ts";
import type { OpenRestaurantHandler } from "../../applications/open-restaurant/open-restaurant.ts";
import type { RestaurantChangeResult } from "../../applications/restaurant-view/restaurant-view.ts";
import type { SetOpeningHoursHandler } from "../../applications/set-opening-hours/set-opening-hours.ts";
import type { SetScheduledOrderSettingsHandler } from "../../applications/set-scheduled-order-settings/set-scheduled-order-settings.ts";
import type {
  PremiumFeeChange,
  UpdateRestaurantHandler,
} from "../../applications/update-restaurant/update-restaurant.ts";
import type { ScheduledOrderSettingsValue } from "../../domains/restaurant/restaurant.ts";
import { toRestaurantMessage } from "./messages.ts";

export type OpeningRpcDependencies = Readonly<{
  closeRestaurant: Pick<CloseRestaurantHandler, "execute">;
  openRestaurant: Pick<OpenRestaurantHandler, "execute">;
  setOpeningHours: Pick<SetOpeningHoursHandler, "execute">;
  setScheduledOrderSettings: Pick<SetScheduledOrderSettingsHandler, "execute">;
  updateRestaurant: Pick<UpdateRestaurantHandler, "execute">;
}>;

type OpeningRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  | "openRestaurant"
  | "closeRestaurant"
  | "updateRestaurant"
  | "setOpeningHours"
  | "setScheduledOrderSettings"
>;

/** premium_fee sets, clear_premium_fee removes, neither keeps (FR-649). */
const premiumFeeChange = (request: {
  premiumFee?: Money;
  clearPremiumFee: boolean;
}): PremiumFeeChange => {
  if (request.premiumFee !== undefined && request.clearPremiumFee) {
    throw new ConnectError(
      "premium_fee and clear_premium_fee are mutually exclusive",
      Code.InvalidArgument,
    );
  }
  if (request.clearPremiumFee) return { kind: "clear" };
  if (request.premiumFee === undefined) return { kind: "keep" };
  return {
    kind: "set",
    fee: {
      amount: request.premiumFee.amount,
      currency: request.premiumFee.currency,
    },
  };
};

/**
 * Exactly one of `settings` and `clear` (FR-672): settings are set together,
 * `clear` stops scheduled orders; neither or both is invalid.
 */
const scheduledOrderSettings = (request: {
  settings?: ScheduledOrderSettings;
  clear: boolean;
}): ScheduledOrderSettingsValue | undefined => {
  if (request.settings !== undefined && request.clear) {
    throw new ConnectError(
      "settings and clear are mutually exclusive",
      Code.InvalidArgument,
    );
  }
  if (request.clear) return undefined;
  const settings = request.settings;
  if (settings === undefined) {
    throw new ConnectError(
      "settings or clear is required",
      Code.InvalidArgument,
    );
  }
  if (
    settings.openingLead === undefined ||
    settings.closingGrace === undefined
  ) {
    throw new ConnectError(
      "settings.opening_lead and settings.closing_grace are required",
      Code.InvalidArgument,
    );
  }
  return {
    slotCapacity: settings.slotCapacity,
    openingLeadMs: durationMs(settings.openingLead),
    closingGraceMs: durationMs(settings.closingGrace),
  };
};

const toOpeningResponse = (result: RestaurantChangeResult) => ({
  restaurant: toRestaurantMessage(result),
  version: BigInt(result.version),
  changed: result.changed,
});

/**
 * Open/Close Restaurant (FR-203): `restaurant#manage`, a repeat is a
 * successful no-op with `changed = false` and no new fact (AC-209).
 */
export const createOpeningRpcs = (
  dependencies: OpeningRpcDependencies,
  subjects: SubjectResolver,
): OpeningRpcs => {
  const openingCommand = async (
    request: { restaurantId: string },
    context: HandlerContext,
  ) => {
    const subject = withRequestRevision(
      await subjects.resolve(context),
      context,
    );
    requireField("restaurant_id", request.restaurantId);
    return { subject, restaurantId: request.restaurantId };
  };
  return {
    async openRestaurant(request, context) {
      try {
        const result = await dependencies.openRestaurant.execute(
          await openingCommand(request, context),
        );
        return create(OpenRestaurantResponseSchema, toOpeningResponse(result));
      } catch (error) {
        throw transportError(error);
      }
    },

    // Update Restaurant (FR-202, FR-649): name and premium fee, `restaurant#manage`.
    async updateRestaurant(request, context) {
      try {
        const { subject, restaurantId } = await openingCommand(
          request,
          context,
        );
        requireField("name", request.name);
        const result = await dependencies.updateRestaurant.execute({
          subject,
          restaurantId,
          name: request.name,
          premiumFee: premiumFeeChange(request),
        });
        return create(
          UpdateRestaurantResponseSchema,
          toOpeningResponse(result),
        );
      } catch (error) {
        throw transportError(error);
      }
    },

    // Set Opening Hours (FR-696): the whole weekly schedule, `restaurant#manage`.
    async setOpeningHours(request, context) {
      try {
        const { subject, restaurantId } = await openingCommand(
          request,
          context,
        );
        const result = await dependencies.setOpeningHours.execute({
          subject,
          restaurantId,
          openingHours: request.openingHours.map((interval) => ({
            day: interval.dayOfWeek,
            opens: interval.opens,
            closes: interval.closes,
          })),
        });
        return create(SetOpeningHoursResponseSchema, toOpeningResponse(result));
      } catch (error) {
        throw transportError(error);
      }
    },

    // Set Scheduled Order Settings (FR-672): set together or cleared, `restaurant#manage`.
    async setScheduledOrderSettings(request, context) {
      try {
        const { subject, restaurantId } = await openingCommand(
          request,
          context,
        );
        const result = await dependencies.setScheduledOrderSettings.execute({
          subject,
          restaurantId,
          settings: scheduledOrderSettings(request),
        });
        return create(
          SetScheduledOrderSettingsResponseSchema,
          toOpeningResponse(result),
        );
      } catch (error) {
        throw transportError(error);
      }
    },

    async closeRestaurant(request, context) {
      try {
        const result = await dependencies.closeRestaurant.execute(
          await openingCommand(request, context),
        );
        return create(CloseRestaurantResponseSchema, toOpeningResponse(result));
      } catch (error) {
        throw transportError(error);
      }
    },
  };
};
