import { create } from "@bufbuild/protobuf";
import type { ServiceImpl } from "@connectrpc/connect";
import { transportError } from "../../../../infrastructure/grpc/errors.ts";
import {
  GetSimulatorResponseSchema,
  type RestaurantsService,
  SetSimulatorEnabledResponseSchema,
} from "../../../../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { withRequestRevision } from "../../../../infrastructure/grpc/revision.ts";
import type { SubjectResolver } from "../../../../infrastructure/grpc/subject.ts";
import type { GetSimulatorHandler } from "../../applications/get/get.ts";
import type { SetSimulatorEnabledHandler } from "../../applications/set-enabled/set-enabled.ts";

export type SimulatorRpcDependencies = Readonly<{
  getSimulator: Pick<GetSimulatorHandler, "execute">;
  setSimulatorEnabled: Pick<SetSimulatorEnabledHandler, "execute">;
}>;

type SimulatorRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  "getSimulator" | "setSimulatorEnabled"
>;

/** Demo Simulator RPCs of operators (FR-604). */
export const createSimulatorRpcs = (
  dependencies: SimulatorRpcDependencies,
  subjects: SubjectResolver,
): SimulatorRpcs => ({
  async getSimulator(_request, context) {
    try {
      const view = await dependencies.getSimulator.execute({
        subject: withRequestRevision(await subjects.resolve(context), context),
      });
      return create(GetSimulatorResponseSchema, { enabled: view.enabled });
    } catch (error) {
      throw transportError(error);
    }
  },

  async setSimulatorEnabled(request, context) {
    try {
      const view = await dependencies.setSimulatorEnabled.execute({
        subject: withRequestRevision(await subjects.resolve(context), context),
        enabled: request.enabled,
      });
      return create(SetSimulatorEnabledResponseSchema, {
        enabled: view.enabled,
      });
    } catch (error) {
      throw transportError(error);
    }
  },
});
