import { requirePermission } from "../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../pkg/authz/subject.ts";
import type { UnitOfWork } from "../../../pkg/unit-of-work/unit-of-work.ts";
import type { Clock } from "../../../pkg/usecase/clock.ts";
import { NotFoundError } from "../../../pkg/usecase/errors.ts";
import type { Cook, CookRepository } from "../domains/cook/cook.ts";
import type { CookPoolAccess } from "./access-control.ts";
import { type CookChangeResult, toCookView } from "./cook-view.ts";
import type { CookWorkload } from "./cook-workload.ts";

export type CookCommand = Readonly<{
  subject: Subject;
  restaurantId: string;
  cookId: string;
}>;

export type ChangeCookDependencies = {
  accessControl: CookPoolAccess;
  unitOfWork: UnitOfWork;
  cooks: CookRepository;
  workload: CookWorkload;
  clock: Clock;
};

/**
 * Shared flow of the line and retirement commands: authorize, then in one
 * Unit of Work load the cook of the restaurant with the order it holds,
 * change it and save. Saving bumps the cook's version, so a concurrent Start
 * Preparation that assigns the same cook conflicts with the change.
 */
export const changeCook = async (
  dependencies: ChangeCookDependencies,
  command: CookCommand,
  action: string,
  change: (
    cook: Cook,
    currentRestaurantOrderId: string | undefined,
    at: Date,
  ) => boolean,
): Promise<CookChangeResult> => {
  await requirePermission(
    dependencies.accessControl.canManageCooks(
      command.subject,
      command.restaurantId,
    ),
    action,
  );
  return dependencies.unitOfWork.run(async () => {
    const cook = await dependencies.cooks.get(command.cookId);
    if (cook === undefined || cook.restaurantId !== command.restaurantId) {
      throw new NotFoundError("Cook", command.cookId);
    }
    const current = (
      await dependencies.workload.activeAssignments(command.restaurantId)
    ).get(cook.id);
    const changed = change(cook, current, dependencies.clock.now());
    if (changed) await dependencies.cooks.save(cook);
    return { ...toCookView(cook, current), changed };
  });
};
