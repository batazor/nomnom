import { requirePermission } from "../../../../pkg/application/require-permission.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import type { SimulatorStateRepository } from "../../domains/simulator/simulator.ts";
import type { SimulatorAccess } from "../access-control.ts";

export type GetSimulator = Readonly<{ subject: Subject }>;

export type SimulatorView = Readonly<{ enabled: boolean }>;

/** Returns the stored Simulator state to an operator (FR-604). */
export class GetSimulatorHandler {
  readonly #accessControl: SimulatorAccess;
  readonly #state: SimulatorStateRepository;

  constructor(dependencies: {
    accessControl: SimulatorAccess;
    state: SimulatorStateRepository;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#state = dependencies.state;
  }

  async execute(query: GetSimulator): Promise<SimulatorView> {
    await requirePermission(
      this.#accessControl.canOperateSimulator(query.subject),
      "get_simulator",
    );
    return { enabled: await this.#state.isEnabled() };
  }
}
