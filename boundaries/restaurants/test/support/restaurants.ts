import type { Clock } from "../../pkg/application/clock.ts";
import type { IdGenerator } from "../../pkg/application/id-generator.ts";
import type { Outbox } from "../../pkg/application/outbox.ts";
import type { Subject } from "../../pkg/application/subject.ts";
import type {
  AuthorizationRevision,
  OwnerRelationshipWriter,
  RestaurantAccessControl,
  StaffRelationshipWriter,
} from "../../slices/restaurant/applications/access-control.ts";
import { AssignStaffHandler } from "../../slices/restaurant/applications/assign-staff/assign-staff.ts";
import { CloseRestaurantHandler } from "../../slices/restaurant/applications/close-restaurant/close-restaurant.ts";
import { OpenRestaurantHandler } from "../../slices/restaurant/applications/open-restaurant/open-restaurant.ts";
import { RegisterRestaurantHandler } from "../../slices/restaurant/applications/register-restaurant/register-restaurant.ts";
import { RevokeStaffHandler } from "../../slices/restaurant/applications/revoke-staff/revoke-staff.ts";
import {
  createRestaurantCollection,
  MongoRestaurantCatalog,
  MongoRestaurantMemberships,
  MongoRestaurantRepository,
} from "../../slices/restaurant/infrastructure/mongodb/restaurants.ts";
import {
  createTestDatabase,
  noKitchenChanges,
  RecordedOutbox,
} from "./mongodb.ts";

export const operator: Subject = { type: "user", id: "operator" };
export const owner: Subject = { type: "user", id: "owner" };
export const stranger: Subject = { type: "user", id: "stranger" };

export const PICKUP_ADDRESS = "1 Pho Street, Hanoi";

export const startOfDay = new Date(Date.UTC(2026, 9, 25, 9));

/**
 * Shared infrastructure of the `restaurants` boundary for tests: a fresh
 * MongoDB database with its Unit of Work and recording Outbox.
 */
export const createInfrastructure = async () => {
  const { mongo, db, unitOfWork } = await createTestDatabase();
  const outbox = new RecordedOutbox(db, unitOfWork);
  const clock = new FixedClock(startOfDay);
  return { mongo, db, unitOfWork, outbox, clock };
};

export type Infrastructure = Awaited<ReturnType<typeof createInfrastructure>>;

export const createRestaurantWorld = async (
  infrastructure?: Infrastructure,
  options: { outbox?: Outbox } = {},
) => {
  infrastructure ??= await createInfrastructure();
  const store = createRestaurantCollection(
    infrastructure.db,
    infrastructure.unitOfWork,
  );
  const restaurants = new MongoRestaurantRepository(store, noKitchenChanges);
  const catalog = new MongoRestaurantCatalog(store);
  const memberships = new MongoRestaurantMemberships(store);
  const authorization = new TestRestaurantAuthorization().allowRegistration(
    operator,
  );
  const ids = new SequentialIdGenerator("restaurant");
  const outbox = options.outbox ?? infrastructure.outbox;
  const changeOpening = {
    accessControl: authorization,
    unitOfWork: infrastructure.unitOfWork,
    restaurants,
    outbox,
    clock: infrastructure.clock,
  };
  const register = new RegisterRestaurantHandler({
    accessControl: authorization,
    ownerRelationships: authorization,
    restaurants,
    ids,
    clock: infrastructure.clock,
  });
  const open = new OpenRestaurantHandler(changeOpening);
  const close = new CloseRestaurantHandler(changeOpening);
  const changeStaff = {
    accessControl: authorization,
    staffRelationships: authorization,
    restaurants,
  };
  const assignStaff = new AssignStaffHandler(changeStaff);
  const revokeStaff = new RevokeStaffHandler(changeStaff);

  /** Registers a restaurant owned by {@link owner}, optionally opened. */
  const givenRestaurant = async (
    name: string,
    state: { open: boolean } = { open: true },
  ): Promise<string> => {
    const { restaurantId } = await register.execute({
      subject: operator,
      name,
      ownerId: owner.id,
      address: PICKUP_ADDRESS,
    });
    if (state.open) {
      await open.execute({ subject: owner, restaurantId });
    }
    return restaurantId;
  };

  return {
    ...infrastructure,
    store,
    restaurants,
    catalog,
    memberships,
    authorization,
    ids,
    register,
    open,
    close,
    assignStaff,
    revokeStaff,
    givenRestaurant,
  };
};

/** Deterministic clock controlled by tests. */
export class FixedClock implements Clock {
  #now: number;

  constructor(now: Date) {
    this.#now = now.getTime();
  }

  now(): Date {
    return new Date(this.#now);
  }

  set(now: Date): void {
    this.#now = now.getTime();
  }

  advance(milliseconds: number): void {
    this.#now += milliseconds;
  }
}

/** Deterministic identifiers `<prefix>-1`, `<prefix>-2`, ... for tests. */
export class SequentialIdGenerator implements IdGenerator {
  readonly #prefix: string;
  #sequence = 0;

  constructor(prefix: string) {
    this.#prefix = prefix;
  }

  next(): string {
    this.#sequence += 1;
    return `${this.#prefix}-${this.#sequence}`;
  }
}

const subjectKey = (subject: Subject): string =>
  `${subject.type}:${subject.id}`;

/**
 * Test authorization graph: explicit registrars and managers; the owner
 * written through {@link OwnerRelationshipWriter} can manage the restaurant;
 * staff written through {@link StaffRelationshipWriter} is recorded.
 */
export class TestRestaurantAuthorization
  implements
    RestaurantAccessControl,
    OwnerRelationshipWriter,
    StaffRelationshipWriter
{
  readonly #registrars = new Set<string>();
  readonly #managers = new Set<string>();
  readonly #owners: Array<{ restaurantId: string; ownerId: string }> = [];
  readonly #staff = new Set<string>();
  /** Every staff write in order, `touch:<restaurantId>:<userId>` or `delete:…`. */
  readonly staffWrites: string[] = [];
  /** Revisions returned by writes: `rev-1`, `rev-2`, … in write order. */
  #revisions = 0;

  /** Revision the last write returned, `undefined` before any write. */
  get lastRevision(): AuthorizationRevision | undefined {
    return this.#revisions === 0 ? undefined : `rev-${this.#revisions}`;
  }

  #nextRevision(): AuthorizationRevision {
    this.#revisions += 1;
    return `rev-${this.#revisions}`;
  }

  allowRegistration(subject: Subject): this {
    this.#registrars.add(subjectKey(subject));
    return this;
  }

  grantManager(subject: Subject, restaurantId: string): this {
    this.#managers.add(`${subjectKey(subject)}@${restaurantId}`);
    return this;
  }

  get owners(): ReadonlyArray<{ restaurantId: string; ownerId: string }> {
    return this.#owners;
  }

  isStaff(restaurantId: string, userId: string): boolean {
    return this.#staff.has(`${restaurantId}:${userId}`);
  }

  async touchStaff(input: {
    restaurantId: string;
    userId: string;
  }): Promise<AuthorizationRevision> {
    this.staffWrites.push(`touch:${input.restaurantId}:${input.userId}`);
    this.#staff.add(`${input.restaurantId}:${input.userId}`);
    return this.#nextRevision();
  }

  async deleteStaff(input: {
    restaurantId: string;
    userId: string;
  }): Promise<AuthorizationRevision> {
    this.staffWrites.push(`delete:${input.restaurantId}:${input.userId}`);
    this.#staff.delete(`${input.restaurantId}:${input.userId}`);
    return this.#nextRevision();
  }

  async canRegisterRestaurant(subject: Subject): Promise<boolean> {
    return this.#registrars.has(subjectKey(subject));
  }

  async canManageRestaurant(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean> {
    return this.#managers.has(`${subjectKey(subject)}@${restaurantId}`);
  }

  async touchOwner(input: {
    restaurantId: string;
    ownerId: string;
  }): Promise<AuthorizationRevision> {
    const exists = this.#owners.some(
      (owner) =>
        owner.restaurantId === input.restaurantId &&
        owner.ownerId === input.ownerId,
    );
    if (!exists) {
      this.#owners.push({ ...input });
    }
    this.grantManager({ type: "user", id: input.ownerId }, input.restaurantId);
    return this.#nextRevision();
  }
}
