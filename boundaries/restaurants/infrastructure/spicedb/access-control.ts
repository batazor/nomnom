import { deadlineInterceptor, v1 } from "@authzed/authzed-node";

import { AuthorizationUnavailableError } from "../../pkg/application/errors.ts";
import type { Subject } from "../../pkg/application/subject.ts";
import type { SpiceDbEnabledConfig } from "../../pkg/config/config.ts";
import type { KitchenAccessControl } from "../../slices/kitchenorder/applications/access-control.ts";
import type { MenuAccessControl } from "../../slices/menu/applications/access-control.ts";
import type { RestaurantAccessControl } from "../../slices/restaurant/applications/access-control.ts";

/** Singleton object of `definition platform` (authorization/schema.zed). */
export const PLATFORM_ID = "nomnom";

/**
 * The single SpiceDB call the adapter needs. The production implementation
 * is the `@authzed/authzed-node` promise client; tests pass a fake.
 */
export interface PermissionsClient {
  checkPermission(
    request: v1.CheckPermissionRequest,
  ): Promise<v1.CheckPermissionResponse>;
}

/**
 * Relationship reads and writes of the `restaurants` writers and the
 * reconciler. `readRelationships` returns one page (the stream collected).
 */
export interface RelationshipsClient {
  writeRelationships(
    request: v1.WriteRelationshipsRequest,
  ): Promise<v1.WriteRelationshipsResponse>;
  readRelationships(
    request: v1.ReadRelationshipsRequest,
  ): Promise<v1.ReadRelationshipsResponse[]>;
}

/** Closable production client created by {@link createSpiceDbClient}. */
export interface SpiceDbClient extends PermissionsClient, RelationshipsClient {
  close(): void;
}

/**
 * Creates the SpiceDB client: preshared key as bearer token, TLS unless
 * `insecure` (plaintext for kind and local runs only), and a per-call gRPC
 * deadline of `timeoutMs`.
 */
export function createSpiceDbClient(
  config: SpiceDbEnabledConfig,
): SpiceDbClient {
  const client = v1.NewClient(
    config.presharedKey,
    config.endpoint,
    config.insecure
      ? v1.ClientSecurity.INSECURE_PLAINTEXT_CREDENTIALS
      : v1.ClientSecurity.SECURE,
    undefined,
    { interceptors: [deadlineInterceptor(config.timeoutMs)] },
  );
  return {
    checkPermission: (request) => client.promises.checkPermission(request),
    writeRelationships: (request) =>
      client.promises.writeRelationships(request),
    readRelationships: (request) => client.promises.readRelationships(request),
    close: () => client.close(),
  };
}

export interface SpiceDbAccessControlOptions {
  readonly client: PermissionsClient;
  /** Upper bound of one decision; exceeding it fails closed. */
  readonly timeoutMs: number;
}

/**
 * SpiceDB adapter of the `restaurants` authorization ports. The port → check
 * mapping follows the normative table of the `add-spicedb-authorization`
 * design. Only `PERMISSIONSHIP_HAS_PERMISSION` allows; no permission and
 * conditional permission deny; any error or timeout throws
 * {@link AuthorizationUnavailableError} (fail closed, gRPC `UNAVAILABLE`).
 */
export class SpiceDbAccessControl
  implements RestaurantAccessControl, MenuAccessControl, KitchenAccessControl
{
  readonly #client: PermissionsClient;
  readonly #timeoutMs: number;

  constructor(options: SpiceDbAccessControlOptions) {
    this.#client = options.client;
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
   * not part of the check.
   */
  canCoordinateFulfillment(
    subject: Subject,
    _restaurantId: string,
  ): Promise<boolean> {
    return this.#check(
      "platform",
      PLATFORM_ID,
      "coordinate_fulfillment",
      subject,
    );
  }

  canViewRestaurantOrders(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean> {
    return this.#check("restaurant", restaurantId, "view_orders", subject);
  }

  async #check(
    resourceType: string,
    resourceId: string,
    permission: string,
    subject: Subject,
  ): Promise<boolean> {
    const request = checkPermissionRequest(
      resourceType,
      resourceId,
      permission,
      subject,
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

/**
 * Rejects when `call` does not settle within `timeoutMs`; the gRPC deadline
 * interceptor bounds the call itself, this bounds clients without it.
 */
export async function withDeadline<T>(
  call: Promise<T>,
  timeoutMs: number,
  operation: string,
): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(
      () =>
        reject(
          new Error(`SpiceDB ${operation} exceeded ${timeoutMs}ms deadline`),
        ),
      timeoutMs,
    );
  });
  try {
    return await Promise.race([call, timeout]);
  } finally {
    clearTimeout(timer);
  }
}

/** Builds `<type>:<id>#<permission>@<subject.type>:<subject.id>` check. */
export function checkPermissionRequest(
  resourceType: string,
  resourceId: string,
  permission: string,
  subject: Subject,
): v1.CheckPermissionRequest {
  return v1.CheckPermissionRequest.create({
    consistency: v1.Consistency.create({
      requirement: { oneofKind: "minimizeLatency", minimizeLatency: true },
    }),
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
