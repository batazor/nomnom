import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import type { CookRepository } from "../../domains/cook/cook.ts";
import type { CookPoolAccess } from "../access-control/access-control.ts";
import { type CookView, toCookView } from "../cook-view/cook-view.ts";
import type { CookWorkload } from "../cook-workload/cook-workload.ts";

export type GetCookPool = Readonly<{ subject: Subject; restaurantId: string }>;

/** Pool Load and cooks of a restaurant (FR-616). */
export type CookPoolView = Readonly<{
  size: number;
  onLine: number;
  busy: number;
  queued: number;
  /** Not retired cooks by name, then by id. */
  cooks: readonly CookView[];
}>;

export class GetCookPoolHandler {
  readonly #accessControl: CookPoolAccess;
  readonly #cooks: CookRepository;
  readonly #workload: CookWorkload;

  constructor(dependencies: {
    accessControl: CookPoolAccess;
    cooks: CookRepository;
    workload: CookWorkload;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#cooks = dependencies.cooks;
    this.#workload = dependencies.workload;
  }

  async execute(query: GetCookPool): Promise<CookPoolView> {
    await requirePermission(
      this.#accessControl.canViewRestaurantOrders(
        query.subject,
        query.restaurantId,
      ),
      "get cook pool",
    );
    const [pool, assignments, queued] = await Promise.all([
      this.#cooks.listPool(query.restaurantId),
      this.#workload.activeAssignments(query.restaurantId),
      this.#workload.queuedOrders(query.restaurantId),
    ]);
    const cooks = pool.map((cook) =>
      toCookView(cook, assignments.get(cook.id)),
    );
    return {
      size: cooks.length,
      onLine: cooks.filter((cook) => cook.onLine).length,
      busy: cooks.filter((cook) => cook.currentRestaurantOrderId !== undefined)
        .length,
      queued,
      cooks,
    };
  }
}
