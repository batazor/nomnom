import { create } from "@bufbuild/protobuf";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  Code,
  ConnectError,
  type HandlerContext,
  type ServiceImpl,
} from "@connectrpc/connect";
import {
  requireField,
  transportError,
} from "../../../../infrastructure/grpc/errors.ts";
import { DeliveryWindowSchema } from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/restaurants_pb.js";
import {
  ConfirmScheduledSlotResponseSchema,
  ListScheduledSlotsResponseSchema,
  ReleaseScheduledSlotResponseSchema,
  ReserveScheduledSlotResponseSchema,
  type RestaurantsService,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { withRequestRevision } from "../../../../infrastructure/grpc/revision.ts";
import type { SubjectResolver } from "../../../../infrastructure/grpc/subject.ts";
import type { ConfirmScheduledSlotHandler } from "../../applications/confirm/confirm.ts";
import type { ListScheduledSlotsHandler } from "../../applications/list/list.ts";
import type { ReleaseScheduledSlotHandler } from "../../applications/release/release.ts";
import type { ReserveScheduledSlotHandler } from "../../applications/reserve/reserve.ts";
import type { DeliveryWindow } from "../../domains/scheduleslot/scheduleslot.ts";

export type ScheduleSlotRpcDependencies = Readonly<{
  confirmScheduledSlot: Pick<ConfirmScheduledSlotHandler, "execute">;
  listScheduledSlots: Pick<ListScheduledSlotsHandler, "execute">;
  releaseScheduledSlot: Pick<ReleaseScheduledSlotHandler, "execute">;
  reserveScheduledSlot: Pick<ReserveScheduledSlotHandler, "execute">;
}>;

/** Delivery Window `[start, end)` of a scheduled order. */
const toDeliveryWindowMessage = (window: DeliveryWindow) =>
  create(DeliveryWindowSchema, {
    start: timestampFromDate(window.start),
    end: timestampFromDate(window.end),
  });

type ScheduleSlotRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  | "listScheduledSlots"
  | "reserveScheduledSlot"
  | "confirmScheduledSlot"
  | "releaseScheduledSlot"
>;

/**
 * Delivery Windows of scheduled orders (FR-673..FR-675, FR-695, FR-703).
 * List Scheduled Slots is public; Reserve, Confirm and Release require the
 * `service` identity of `orders`. A full window is a business answer
 * (`slot_unavailable`), not a gRPC error.
 */
export const createScheduleSlotRpcs = (
  dependencies: ScheduleSlotRpcDependencies,
  subjects: SubjectResolver,
): ScheduleSlotRpcs => {
  const caller = async (context: HandlerContext) =>
    withRequestRevision(await subjects.resolve(context), context);
  return {
    async listScheduledSlots(request) {
      try {
        requireField("restaurant_id", request.restaurantId);
        const slots = await dependencies.listScheduledSlots.execute({
          restaurantId: request.restaurantId,
        });
        return create(ListScheduledSlotsResponseSchema, {
          slots: slots.map((slot) => ({
            window: toDeliveryWindowMessage(slot.window),
            available: slot.available,
          })),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async reserveScheduledSlot(request, context) {
      try {
        const subject = await caller(context);
        requireField("restaurant_id", request.restaurantId);
        requireField("order_id", request.orderId);
        if (request.windowStart === undefined) {
          throw new ConnectError(
            "window_start is required",
            Code.InvalidArgument,
          );
        }
        const result = await dependencies.reserveScheduledSlot.execute({
          subject,
          restaurantId: request.restaurantId,
          windowStart: timestampDate(request.windowStart),
          orderId: request.orderId,
        });
        return create(ReserveScheduledSlotResponseSchema, {
          result:
            result.outcome === "reserved"
              ? {
                  case: "reserved",
                  value: toDeliveryWindowMessage(result.window),
                }
              : {
                  case: "slotUnavailable",
                  value: {
                    alternatives: result.alternatives.map(
                      toDeliveryWindowMessage,
                    ),
                  },
                },
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async confirmScheduledSlot(request, context) {
      try {
        const subject = await caller(context);
        requireField("order_id", request.orderId);
        const result = await dependencies.confirmScheduledSlot.execute({
          subject,
          orderId: request.orderId,
        });
        return create(ConfirmScheduledSlotResponseSchema, {
          result:
            result.outcome === "confirmed"
              ? {
                  case: "confirmed",
                  value: toDeliveryWindowMessage(result.window),
                }
              : { case: "slotUnavailable", value: { alternatives: [] } },
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async releaseScheduledSlot(request, context) {
      try {
        const subject = await caller(context);
        requireField("order_id", request.orderId);
        await dependencies.releaseScheduledSlot.execute({
          subject,
          orderId: request.orderId,
        });
        return create(ReleaseScheduledSlotResponseSchema, {});
      } catch (error) {
        throw transportError(error);
      }
    },
  };
};
