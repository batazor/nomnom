import type { Outbox } from "../../pkg/application/outbox.ts";
import type { Subject } from "../../pkg/application/subject.ts";
import { FixedClock } from "../../pkg/infrastructure/memory/clock.ts";
import { SequentialIdGenerator } from "../../pkg/infrastructure/memory/id-generator.ts";
import { InMemoryOutbox } from "../../pkg/infrastructure/memory/outbox.ts";
import { InMemoryUnitOfWork } from "../../pkg/infrastructure/memory/unit-of-work.ts";
import { AssignStaffHandler } from "../../slices/restaurant/applications/assign-staff/assign-staff.ts";
import { CloseRestaurantHandler } from "../../slices/restaurant/applications/close-restaurant/close-restaurant.ts";
import { OpenRestaurantHandler } from "../../slices/restaurant/applications/open-restaurant/open-restaurant.ts";
import { RegisterRestaurantHandler } from "../../slices/restaurant/applications/register-restaurant/register-restaurant.ts";
import { RevokeStaffHandler } from "../../slices/restaurant/applications/revoke-staff/revoke-staff.ts";
import {
  createRestaurantStore,
  InMemoryRestaurantAuthorization,
  InMemoryRestaurantCatalog,
  InMemoryRestaurantRepository,
} from "../../slices/restaurant/infrastructure/memory/restaurants.ts";

export const operator: Subject = { type: "user", id: "operator" };
export const owner: Subject = { type: "user", id: "owner" };
export const stranger: Subject = { type: "user", id: "stranger" };

export const PICKUP_ADDRESS = "1 Pho Street, Hanoi";

export const startOfDay = new Date(Date.UTC(2026, 9, 25, 9));

/** Shared in-memory infrastructure of the `restaurants` boundary for tests. */
export const createInfrastructure = () => {
  const unitOfWork = new InMemoryUnitOfWork();
  const outbox = new InMemoryOutbox(unitOfWork);
  const clock = new FixedClock(startOfDay);
  return { unitOfWork, outbox, clock };
};

export type Infrastructure = ReturnType<typeof createInfrastructure>;

export const createRestaurantWorld = (
  infrastructure: Infrastructure = createInfrastructure(),
  options: { outbox?: Outbox } = {},
) => {
  const store = createRestaurantStore();
  infrastructure.unitOfWork.enlist(store);
  const restaurants = new InMemoryRestaurantRepository(store);
  const catalog = new InMemoryRestaurantCatalog(store);
  const authorization = new InMemoryRestaurantAuthorization().allowRegistration(
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
