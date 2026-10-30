import type { Subject } from "../../../../pkg/authz/subject.ts";

/**
 * Authorization port of the `simulator` slice: `platform#operate_simulator`
 * (FR-604). Implementations throw when no decision can be obtained (fail
 * closed).
 */
export interface SimulatorAccess {
  canOperateSimulator(subject: Subject): Promise<boolean>;
}
