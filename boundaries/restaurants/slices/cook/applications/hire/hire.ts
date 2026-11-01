import { randomUUID } from "node:crypto";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import { Cook, type CookRepository } from "../../domains/cook/cook.ts";
import type { CookPoolAccess } from "../access-control/access-control.ts";
import { type CookView, toCookView } from "../cook-view/cook-view.ts";
import type { CookRestaurants } from "../cook-workload/cook-workload.ts";
import { NotFoundError, PermissionDeniedError } from "../failure/failure.ts";

export type HireCook = Readonly<{
  subject: Subject;
  restaurantId: string;
  name: string;
}>;

/** Adds a cook to the Cook Pool of the restaurant, off line (FR-614). */
export class HireCookHandler {
  readonly #accessControl: CookPoolAccess;
  readonly #restaurants: CookRestaurants;
  readonly #cooks: CookRepository;

  constructor(dependencies: {
    accessControl: CookPoolAccess;
    restaurants: CookRestaurants;
    cooks: CookRepository;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#restaurants = dependencies.restaurants;
    this.#cooks = dependencies.cooks;
  }

  async execute(command: HireCook): Promise<CookView> {
    if (
      !(await this.#accessControl.canManageCooks(
        command.subject,
        command.restaurantId,
      ))
    ) {
      throw new PermissionDeniedError("hire cook");
    }
    const cook = Cook.hire({
      id: randomUUID(),
      restaurantId: command.restaurantId,
      name: command.name,
    });
    if (!(await this.#restaurants.exists(command.restaurantId))) {
      throw new NotFoundError("Restaurant", command.restaurantId);
    }
    await this.#cooks.save(cook);
    return toCookView(cook, undefined);
  }
}
