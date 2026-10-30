import { v1 } from "@authzed/authzed-node";

import { AuthorizationUnavailableError } from "../../pkg/authz/errors.ts";
import type { Subject } from "../../pkg/authz/subject.ts";
import type { CookPoolAccess } from "../../slices/cook/applications/access-control/access-control.ts";
import type { MenuAccessControl } from "../../slices/menu/applications/access-control/access-control.ts";
import type { RestaurantAccessControl } from "../../slices/restaurant/applications/access-control/access-control.ts";
import type {
  FulfillmentCoordinatorAccess,
  FulfillmentProcessAccess,
  KitchenStaffAccess,
  RestaurantOrderViewAccess,
} from "../../slices/restaurantorder/applications/access-control/access-control.ts";
import type { SlotReservationAccess } from "../../slices/scheduleslot/applications/access-control/access-control.ts";
import type { SimulatorAccess } from "../../slices/simulator/applications/access-control/access-control.ts";
import {
  checkConsistency,
  type PermissionsClient,
  withDeadline,
} from "./client.ts";

/** Singleton object of `definition platform` (authorization/schema.zed). */
export const PLATFORM_ID = "nomnom";

/**
 * Reads the authorization revision (ZedToken) stored with a restaurant;
 * `undefined` when none is stored or the restaurant does not exist.
 */
export interface RevisionReader {
  authorizationRevision(restaurantId: string): Promise<string | undefined>;
}

export interface SpiceDbAccessControlOptions {
  readonly client: PermissionsClient;
  /** Stored revisions of `restaurant:<id>` (FR-447). */
  readonly revisions: RevisionReader;
  /** Upper bound of one decision; exceeding it fails closed. */
  readonly timeoutMs: number;
}

/**
 * SpiceDB adapter of the `restaurants` authorization ports. The port → check
 * mapping follows the normative table of the `add-spicedb-authorization`
 * design. Only `PERMISSIONSHIP_HAS_PERMISSION` allows; no permission and
 * conditional permission deny; any error or timeout throws
 * {@link AuthorizationUnavailableError} (fail closed, gRPC `UNAVAILABLE`).
 *
 * Consistency (design of `add-authorization-revisions`): a `restaurant:<id>`
 * check uses `at_least_as_fresh` with the restaurant's stored revision, so it
 * sees the restaurant's own relationship writes. `platform:nomnom` and a
 * restaurant without a stored revision use the caller's revision from the
 * `authorization-revision` metadata, otherwise `minimize_latency`. A failed revision read fails
 * closed like a failed check.
 */
export class SpiceDbAccessControl
  implements
    RestaurantAccessControl,
    MenuAccessControl,
    KitchenStaffAccess,
    FulfillmentCoordinatorAccess,
    FulfillmentProcessAccess,
    SlotReservationAccess,
    RestaurantOrderViewAccess,
    SimulatorAccess,
    CookPoolAccess
{
  readonly #client: PermissionsClient;
  readonly #revisions: RevisionReader;
  readonly #timeoutMs: number;

  constructor(options: SpiceDbAccessControlOptions) {
    this.#client = options.client;
    this.#revisions = options.revisions;
    this.#timeoutMs = options.timeoutMs;
  }

  canRegisterRestaurant(subject: Subject): Promise<boolean> {
    return this.#check("platform", PLATFORM_ID, "register_restaurant", subject);
  }

  canManageRestaurant(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean> {
    return this.#check("restaurant", restaurantId, "manage", subject);
  }

  canManageMenu(subject: Subject, restaurantId: string): Promise<boolean> {
    return this.#check("restaurant", restaurantId, "manage_menu", subject);
  }

  canOperateKitchen(subject: Subject, restaurantId: string): Promise<boolean> {
    return this.#check("restaurant", restaurantId, "operate_kitchen", subject);
  }

  /**
   * Platform capability: the fulfillment service addresses Restaurant Orders
   * before any restaurant-level relationship exists, so the restaurant id is
   * not part of the check. Commands that name only an order or a Restaurant
   * Order (Queue Restaurant Order, Confirm and Release Scheduled Slot) make
   * the same check without one.
   */
  canCoordinateFulfillment(
    subject: Subject,
    _restaurantId?: string,
  ): Promise<boolean> {
    return this.#check(
      "platform",
      PLATFORM_ID,
      "coordinate_fulfillment",
      subject,
    );
  }

  canManageCooks(subject: Subject, restaurantId: string): Promise<boolean> {
    return this.#check("restaurant", restaurantId, "manage_cooks", subject);
  }

  canViewRestaurantOrders(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean> {
    return this.#check("restaurant", restaurantId, "view_orders", subject);
  }

  /** Demo Simulator of the platform (FR-604): operators only. */
  canOperateSimulator(subject: Subject): Promise<boolean> {
    return this.#check("platform", PLATFORM_ID, "operate_simulator", subject);
  }

  async #check(
    resourceType: string,
    resourceId: string,
    permission: string,
    subject: Subject,
  ): Promise<boolean> {
    // A resource reads at least as fresh as its own stored revision; the
    // platform and a resource without one use the caller's revision, if any.
    let revision = subject.authorizationRevision;
    if (resourceType === "restaurant") {
      try {
        revision =
          (await this.#revisions.authorizationRevision(resourceId)) ?? revision;
      } catch (error) {
        throw new AuthorizationUnavailableError(error);
      }
    }
    const request = checkPermissionRequest(
      resourceType,
      resourceId,
      permission,
      subject,
      revision,
    );
    let response: v1.CheckPermissionResponse;
    try {
      response = await withDeadline(
        this.#client.checkPermission(request),
        this.#timeoutMs,
        "check",
      );
    } catch (error) {
      throw new AuthorizationUnavailableError(error);
    }
    return (
      response.permissionship ===
      v1.CheckPermissionResponse_Permissionship.HAS_PERMISSION
    );
  }
}

/** Builds `<type>:<id>#<permission>@<subject.type>:<subject.id>` check. */
export function checkPermissionRequest(
  resourceType: string,
  resourceId: string,
  permission: string,
  subject: Subject,
  revision?: string,
): v1.CheckPermissionRequest {
  return v1.CheckPermissionRequest.create({
    consistency: checkConsistency(revision),
    resource: v1.ObjectReference.create({
      objectType: resourceType,
      objectId: resourceId,
    }),
    permission,
    subject: v1.SubjectReference.create({
      object: v1.ObjectReference.create({
        objectType: subject.type,
        objectId: subject.id,
      }),
    }),
  });
}
