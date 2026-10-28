import type { Clock } from "../../../../pkg/application/clock.ts";
import type { IdGenerator } from "../../../../pkg/application/id-generator.ts";
import { requirePermission } from "../../../../pkg/application/require-permission.ts";
import type { Subject } from "../../../../pkg/application/subject.ts";
import {
  Restaurant,
  type RestaurantRepository,
} from "../../domains/restaurant/restaurant.ts";
import type {
  OwnerRelationshipWriter,
  RestaurantAccessControl,
} from "../access-control.ts";
import { type RestaurantView, toRestaurantView } from "../restaurant-view.ts";

export type RegisterRestaurant = Readonly<{
  subject: Subject;
  name: string;
  ownerId: string;
  /** Required (ADR 41): Market of the new restaurant, immutable. */
  marketId: string;
  /** Required (FR-201): IANA time zone of the new restaurant, immutable. */
  timeZone: string;
}>;

export type RegisterRestaurantResult = RestaurantView &
  Readonly<{ version: number }>;

export class RegisterRestaurantHandler {
  readonly #accessControl: RestaurantAccessControl;
  readonly #ownerRelationships: OwnerRelationshipWriter;
  readonly #restaurants: RestaurantRepository;
  readonly #ids: IdGenerator;
  readonly #clock: Clock;

  constructor(dependencies: {
    accessControl: RestaurantAccessControl;
    ownerRelationships: OwnerRelationshipWriter;
    restaurants: RestaurantRepository;
    ids: IdGenerator;
    clock: Clock;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#ownerRelationships = dependencies.ownerRelationships;
    this.#restaurants = dependencies.restaurants;
    this.#ids = dependencies.ids;
    this.#clock = dependencies.clock;
  }

  async execute(
    command: RegisterRestaurant,
  ): Promise<RegisterRestaurantResult> {
    await requirePermission(
      this.#accessControl.canRegisterRestaurant(command.subject),
      "register restaurant",
    );
    const restaurant = Restaurant.register({
      id: this.#ids.next(),
      name: command.name,
      ownerId: command.ownerId,
      marketId: command.marketId,
      timeZone: command.timeZone,
      registeredAt: this.#clock.now(),
    });
    const revision = await this.#ownerRelationships.touchOwner({
      restaurantId: restaurant.id,
      ownerId: restaurant.ownerId,
    });
    // The owner can manage the restaurant right after registration (FR-447).
    restaurant.recordAuthorizationRevision(revision);
    const version = await this.#restaurants.save(restaurant);
    restaurant.pullEvents();
    return { ...toRestaurantView(restaurant), version };
  }
}
