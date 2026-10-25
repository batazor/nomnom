import type { Clock } from "../../../../pkg/application/clock.ts";
import { requirePermission } from "../../../../pkg/application/require-permission.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import type { SimulatorStateRepository } from "../../domains/simulator/simulator.ts";
import type { SimulatorAccess } from "../access-control.ts";
import type { SimulatorView } from "../get-simulator/get-simulator.ts";

export type SetSimulatorEnabled = Readonly<{
  subject: Subject;
  enabled: boolean;
}>;

/** Stores the Simulator state set by an operator (FR-604). */
export class SetSimulatorEnabledHandler {
  readonly #accessControl: SimulatorAccess;
  readonly #state: SimulatorStateRepository;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: SimulatorAccess;
    state: SimulatorStateRepository;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#state = dependencies.state;
    this.#clock = dependencies.clock;
  }

  async execute(command: SetSimulatorEnabled): Promise<SimulatorView> {
    await requirePermission(
      this.#accessControl.canOperateSimulator(command.subject),
      "set_simulator_enabled",
    );
    await this.#state.setEnabled(command.enabled, this.#clock.now());
    return { enabled: command.enabled };
  }
}
