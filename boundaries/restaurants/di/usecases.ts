// Use case telemetry of the composition roots (docs/observability.md, "Use
// cases"): after the slices registered their handlers, the process replaces
// each listed registration with the same handler decorated by
// UseCaseTelemetry (pkg/telemetry/usecase.ts). Slices and use cases stay
// unaware of OpenTelemetry. Names are the stable snake_case names of the
// application folders.
import { type AwilixContainer, asFunction } from "awilix";

import {
  command,
  query,
  type UseCase,
  type UseCaseTelemetry,
} from "../pkg/telemetry/usecase.ts";

/** Use cases of `cmd/restaurants` by cradle key. */
export const RESTAURANTS_USE_CASES = {
  getRestaurant: query("get_restaurant"),
  batchGetRestaurants: query("batch_get_restaurants"),
  listRestaurants: query("list_restaurants"),
  listOwnedRestaurants: query("list_owned_restaurants"),
  registerRestaurant: command("register_restaurant"),
  updateRestaurant: command("update_restaurant"),
  assignStaff: command("assign_staff"),
  revokeStaff: command("revoke_staff"),
  openRestaurant: command("open_restaurant"),
  closeRestaurant: command("close_restaurant"),
  getMenu: query("get_menu"),
  batchGetMenus: query("batch_get_menus"),
  createMenu: command("create_menu"),
  addMenuItem: command("add_menu_item"),
  updateMenuItem: command("update_menu_item"),
  setMenuItemAvailability: command("set_menu_item_availability"),
  receiveOrderRequest: command("receive_order_request"),
  cancelRestaurantOrder: command("cancel_restaurant_order"),
  acceptRestaurantOrder: command("accept_order"),
  rejectRestaurantOrder: command("reject_order"),
  startPreparation: command("start_preparation"),
  markRestaurantOrderAssembled: command("mark_assembled"),
  markRestaurantOrderReady: command("mark_ready_for_pickup"),
  reportPreparationDelay: command("report_preparation_delay"),
  reportPreparationFailure: command("report_preparation_failure"),
  hireCook: command("hire_cook"),
  putCookOnLine: command("put_cook_on_line"),
  takeCookOffLine: command("take_cook_off_line"),
  retireCook: command("retire_cook"),
  getCookPool: query("get_cook_pool"),
  getSimulator: query("get_simulator"),
  setSimulatorEnabled: command("set_simulator_enabled"),
} as const;

/** Use cases of `cmd/restaurants-consumer` by cradle key. */
export const CONSUMER_USE_CASES = {
  recordPickup: command("record_pickup"),
  recordWaitTime: command("record_wait_time"),
} as const;

type Executable = { execute(input: never): Promise<unknown> };

/**
 * Decorates the registered handlers `useCases` of `container` with the
 * `useCaseTelemetry` registration; the lifetime stays singleton.
 */
export function instrumentUseCases<
  C extends { readonly useCaseTelemetry: UseCaseTelemetry },
>(
  container: AwilixContainer<C>,
  useCases: Readonly<Partial<Record<keyof C, UseCase>>>,
): void {
  for (const [key, useCase] of Object.entries(useCases) as [
    keyof C & string,
    UseCase,
  ][]) {
    const original = container.registrations[key];
    if (original === undefined) {
      throw new Error(`use case ${key} is not registered`);
    }
    container.register(
      key,
      asFunction(({ useCaseTelemetry }: C) =>
        useCaseTelemetry.instrument(
          useCase,
          original.resolve(container) as Executable,
        ),
      ).singleton(),
    );
  }
}
