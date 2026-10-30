import { requirePermission } from "../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../pkg/authz/subject.ts";
import { NotFoundError } from "../../../../pkg/usecase/errors.ts";
import type { IdGenerator } from "../../../../pkg/usecase/id-generator.ts";
import { Cook, type CookRepository } from "../../domains/cook/cook.ts";
import type { CookPoolAccess } from "../access-control.ts";
import { type CookView, toCookView } from "../cook-view.ts";
import type { CookRestaurants } from "../cook-workload.ts";

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
  readonly #ids: IdGenerator;

  constructor(dependencies: {
    accessControl: CookPoolAccess;
    restaurants: CookRestaurants;
    cooks: CookRepository;
    ids: IdGenerator;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#restaurants = dependencies.restaurants;
    this.#cooks = dependencies.cooks;
    this.#ids = dependencies.ids;
  }

  async execute(command: HireCook): Promise<CookView> {
    await requirePermission(
      this.#accessControl.canManageCooks(command.subject, command.restaurantId),
      "hire cook",
    );
    const cook = Cook.hire({
      id: this.#ids.next(),
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
