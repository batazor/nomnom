import {
  type ChangeStaff,
  type ChangeStaffDependencies,
  changeStaff,
  type StaffChangeResult,
} from "../change-staff/change-staff.ts";

export type AssignStaff = ChangeStaff;

/**
 * Adds a user to the restaurant staff and writes
 * `TOUCH restaurant:<id>#staff@user:<userId>` before saving. Requires
 * `restaurant#manage`; assigning existing staff is a no-op.
 */
export class AssignStaffHandler {
  readonly #dependencies: ChangeStaffDependencies;

  constructor(dependencies: ChangeStaffDependencies) {
    this.#dependencies = dependencies;
  }

  execute(command: AssignStaff): Promise<StaffChangeResult> {
    return changeStaff(
      this.#dependencies,
      command,
      "assign staff",
      (restaurant, userId) => restaurant.assignStaff(userId),
      (writer, input) => writer.touchStaff(input),
    );
  }
}
