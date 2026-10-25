import {
  type ChangeStaff,
  type ChangeStaffDependencies,
  changeStaff,
  type StaffChangeResult,
} from "../change-staff.ts";

export type RevokeStaff = ChangeStaff;

/**
 * Removes a user from the restaurant staff and writes
 * `DELETE restaurant:<id>#staff@user:<userId>` before saving. Requires
 * `restaurant#manage`; revoking a user who is not staff is a no-op. The owner
 * keeps `restaurant#owner` regardless of the staff set.
 */
export class RevokeStaffHandler {
  readonly #dependencies: ChangeStaffDependencies;

  constructor(dependencies: ChangeStaffDependencies) {
    this.#dependencies = dependencies;
  }

  execute(command: RevokeStaff): Promise<StaffChangeResult> {
    return changeStaff(
      this.#dependencies,
      command,
      "revoke staff",
      (restaurant, userId) => restaurant.revokeStaff(userId),
      (writer, input) => writer.deleteStaff(input),
    );
  }
}
