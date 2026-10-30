import { create } from "@bufbuild/protobuf";
import { timestampDate } from "@bufbuild/protobuf/wkt";
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
import {
  AcceptRestaurantOrderResponseSchema,
  MarkRestaurantOrderAssembledResponseSchema,
  MarkRestaurantOrderReadyResponseSchema,
  RejectRestaurantOrderResponseSchema,
  ReportPreparationDelayResponseSchema,
  ReportPreparationFailureResponseSchema,
  type RestaurantsService,
  StartNextPreparationResponseSchema,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { withRequestRevision } from "../../../../infrastructure/grpc/revision.ts";
import type { SubjectResolver } from "../../../../infrastructure/grpc/subject.ts";
import type { AcceptOrderHandler } from "../../applications/acceptance/accept-order/accept-order.ts";
import type { RejectOrderHandler } from "../../applications/acceptance/reject-order/reject-order.ts";
import type { KitchenCommand } from "../../applications/kitchen-command/kitchen-command.ts";
import type { MarkAssembledHandler } from "../../applications/preparation/mark-assembled/mark-assembled.ts";
import type { MarkReadyForPickupHandler } from "../../applications/preparation/mark-ready-for-pickup/mark-ready-for-pickup.ts";
import type { ReportPreparationDelayHandler } from "../../applications/preparation/report-preparation-delay/report-preparation-delay.ts";
import type { ReportPreparationFailureHandler } from "../../applications/preparation/report-preparation-failure/report-preparation-failure.ts";
import type { StartPreparationHandler } from "../../applications/preparation/start-preparation/start-preparation.ts";
import { toRestaurantOrderMessage } from "./messages.ts";

export type KitchenRpcDependencies = Readonly<{
  acceptRestaurantOrder: Pick<AcceptOrderHandler, "execute">;
  markRestaurantOrderAssembled: Pick<MarkAssembledHandler, "execute">;
  markRestaurantOrderReady: Pick<MarkReadyForPickupHandler, "execute">;
  rejectRestaurantOrder: Pick<RejectOrderHandler, "execute">;
  reportPreparationDelay: Pick<ReportPreparationDelayHandler, "execute">;
  reportPreparationFailure: Pick<ReportPreparationFailureHandler, "execute">;
  startPreparation: Pick<StartPreparationHandler, "execute">;
}>;

type KitchenRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  | "acceptRestaurantOrder"
  | "rejectRestaurantOrder"
  | "startNextPreparation"
  | "markRestaurantOrderAssembled"
  | "markRestaurantOrderReady"
  | "reportPreparationDelay"
  | "reportPreparationFailure"
>;

/** Kitchen command RPCs over one addressed Restaurant Order. */
export const createKitchenRpcs = (
  dependencies: KitchenRpcDependencies,
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
        const view = await dependencies.acceptRestaurantOrder.execute({
          ...command,
          expectedReadyAt:
            request.expectedReadyAt === undefined
              ? undefined
              : timestampDate(request.expectedReadyAt),
        });
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

    // FR-225: the Kitchen Queue chooses the order; an empty queue starts
    // nothing and answers without restaurant_order.
    async startNextPreparation(request, context) {
      try {
        const subject = withRequestRevision(
          await subjects.resolve(context),
          context,
        );
        requireField("restaurant_id", request.restaurantId);
        requireField("request_id", request.requestId);
        const view = await dependencies.startPreparation.execute({
          subject,
          restaurantId: request.restaurantId,
          requestId: request.requestId,
        });
        return create(
          StartNextPreparationResponseSchema,
          view === undefined
            ? {}
            : { restaurantOrder: toRestaurantOrderMessage(view) },
        );
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
