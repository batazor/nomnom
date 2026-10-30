import type { CookAssigner } from "../../../restaurantorder/applications/start-preparation/start-preparation.ts";
import { NoCookAvailableError } from "../../../restaurantorder/domains/restaurantorder/restaurantorder.ts";
import type { Cook, CookRepository } from "../../domains/cook/cook.ts";
import type { CookWorkload } from "../cook-workload/cook-workload.ts";

/** Never assigned cooks first, then the earliest last assignment, then id. */
const byLastAssignment = (left: Cook, right: Cook): number => {
  const leftAt = left.lastAssignedAt?.getTime() ?? Number.NEGATIVE_INFINITY;
  const rightAt = right.lastAssignedAt?.getTime() ?? Number.NEGATIVE_INFINITY;
  if (leftAt !== rightAt) return leftAt < rightAt ? -1 : 1;
  return left.id < right.id ? -1 : left.id > right.id ? 1 : 0;
};

/**
 * Assignment of a cook at Start Preparation (FR-225): the free cook on line
 * of the restaurant with the earliest last assignment. Runs inside the Unit
 * of Work of the command and saves the cook, so two starts that pick the same
 * cook conflict on its document and the loser retries with the next one.
 */
export class PoolCookAssigner implements CookAssigner {
  readonly #cooks: CookRepository;
  readonly #workload: CookWorkload;

  constructor(dependencies: { cooks: CookRepository; workload: CookWorkload }) {
    this.#cooks = dependencies.cooks;
    this.#workload = dependencies.workload;
  }

  async assignFreeCook(restaurantId: string, at: Date): Promise<string> {
    // One session of the transaction: reads run one after another.
    const pool = await this.#cooks.listPool(restaurantId);
    const assignments = await this.#workload.activeAssignments(restaurantId);
    const cook = pool
      .filter((candidate) => candidate.onLine && !assignments.has(candidate.id))
      .sort(byLastAssignment)[0];
    if (cook === undefined) throw new NoCookAvailableError(restaurantId);
    cook.assign({ assignedAt: at });
    await this.#cooks.save(cook);
    return cook.id;
  }
}
