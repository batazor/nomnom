import { create } from "@bufbuild/protobuf";
import type { ServiceImpl } from "@connectrpc/connect";
import { transportError } from "./errors.ts";
import {
  GetSimulatorResponseSchema,
  type RestaurantsService,
  SetSimulatorEnabledResponseSchema,
} from "./gen/nomnom/restaurants/v1/service_pb.js";
import { withRequestRevision } from "./revision.ts";
import type { RestaurantsServiceDependencies } from "./service.ts";
import type { SubjectResolver } from "./subject.ts";

type SimulatorRpcs = Pick<
  ServiceImpl<typeof RestaurantsService>,
  "getSimulator" | "setSimulatorEnabled"
>;

/** Demo Simulator RPCs of operators (FR-604). */
export const createSimulatorRpcs = (
  dependencies: RestaurantsServiceDependencies,
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
