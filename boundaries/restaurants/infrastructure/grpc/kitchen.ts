import { create } from "@bufbuild/protobuf";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import {
  Code,
  ConnectError,
  type HandlerContext,
  type ServiceImpl,
} from "@connectrpc/connect";
import type { KitchenCommand } from "../../slices/restaurantorder/applications/kitchen-command.ts";
import { requireField, transportError } from "./errors.ts";
import {
  AcceptRestaurantOrderResponseSchema,
  MarkRestaurantOrderAssembledResponseSchema,
  MarkRestaurantOrderReadyResponseSchema,
  RejectRestaurantOrderResponseSchema,
  ReportPreparationDelayResponseSchema,
  ReportPreparationFailureResponseSchema,
  type RestaurantsService,
  StartPreparationResponseSchema,
} from "./gen/nomnom/restaurants/v1/service_pb.js";
import { toRestaurantOrderMessage } from "./messages.ts";
import { withRequestRevision } from "./revision.ts";
import type { RestaurantsServiceDependencies } from "./service.ts";
import type { SubjectResolver } from "./subject.ts";

type KitchenRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  | "acceptRestaurantOrder"
  | "rejectRestaurantOrder"
  | "startPreparation"
  | "markRestaurantOrderAssembled"
  | "markRestaurantOrderReady"
  | "reportPreparationDelay"
  | "reportPreparationFailure"
>;

/** Kitchen command RPCs over one addressed Restaurant Order. */
export const createKitchenRpcs = (
  dependencies: RestaurantsServiceDependencies,
  subjects: SubjectResolver,
): KitchenRpcs => {
  /** Resolves the caller and the addressed Restaurant Order of a kitchen command. */
  const kitchenCommand = async (
    request: { restaurantId: string; restaurantOrderId: string },
    context: HandlerContext,
  ): Promise<KitchenCommand> => {
    const subject = withRequestRevision(
      await subjects.resolve(context),
      context,
    );
    requireField("restaurant_id", request.restaurantId);
    requireField("restaurant_order_id", request.restaurantOrderId);
    return {
      subject,
      restaurantId: request.restaurantId,
      restaurantOrderId: request.restaurantOrderId,
    };
  };
  return {
    async acceptRestaurantOrder(request, context) {
      try {
        const command = await kitchenCommand(request, context);
        const view = await dependencies.acceptRestaurantOrder.execute(command);
        return create(AcceptRestaurantOrderResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async rejectRestaurantOrder(request, context) {
      try {
        const command = await kitchenCommand(request, context);
        const view = await dependencies.rejectRestaurantOrder.execute({
          ...command,
          reason: request.reason === "" ? undefined : request.reason,
        });
        return create(RejectRestaurantOrderResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async startPreparation(request, context) {
      try {
        const command = await kitchenCommand(request, context);
        const view = await dependencies.startPreparation.execute(command);
        return create(StartPreparationResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async markRestaurantOrderAssembled(request, context) {
      try {
        const command = await kitchenCommand(request, context);
        const view =
          await dependencies.markRestaurantOrderAssembled.execute(command);
        return create(MarkRestaurantOrderAssembledResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async markRestaurantOrderReady(request, context) {
      try {
        const command = await kitchenCommand(request, context);
        const view =
          await dependencies.markRestaurantOrderReady.execute(command);
        return create(MarkRestaurantOrderReadyResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async reportPreparationDelay(request, context) {
      try {
        const command = await kitchenCommand(request, context);
        if (request.expectedReadyAt === undefined) {
          throw new ConnectError(
            "expected_ready_at is required",
            Code.InvalidArgument,
          );
        }
        const view = await dependencies.reportPreparationDelay.execute({
          ...command,
          expectedReadyAt: timestampDate(request.expectedReadyAt),
          reason: request.reason === "" ? undefined : request.reason,
        });
        return create(ReportPreparationDelayResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },

    async reportPreparationFailure(request, context) {
      try {
        const command = await kitchenCommand(request, context);
        requireField("reason", request.reason);
        const view = await dependencies.reportPreparationFailure.execute({
          ...command,
          reason: request.reason,
        });
        return create(ReportPreparationFailureResponseSchema, {
          restaurantOrder: toRestaurantOrderMessage(view),
        });
      } catch (error) {
        throw transportError(error);
      }
    },
  };
};
