import { v1 } from "@authzed/authzed-node";

import { AuthorizationUnavailableError } from "../../pkg/authz/errors.ts";
import type {
  AuthorizationRevision,
  OwnerRelationshipWriter,
  StaffRelationshipWriter,
} from "../../slices/restaurant/applications/access-control.ts";
import { type RelationshipsClient, withDeadline } from "./client.ts";

export const RESTAURANT = "restaurant";
export const USER = "user";

/** Relations of `definition restaurant` owned by the `restaurants` context. */
export type RestaurantRelation = "owner" | "staff";

export type RelationshipOperation = "touch" | "delete";

const OPERATIONS = {
  touch: v1.RelationshipUpdate_Operation.TOUCH,
  delete: v1.RelationshipUpdate_Operation.DELETE,
} as const;

/** Builds a single-update `restaurant:<id>#<relation>@user:<userId>` write. */
export function restaurantRelationshipWrite(
  operation: RelationshipOperation,
  relation: RestaurantRelation,
  restaurantId: string,
  userId: string,
): v1.WriteRelationshipsRequest {
  return v1.WriteRelationshipsRequest.create({
    updates: [
      v1.RelationshipUpdate.create({
        operation: OPERATIONS[operation],
        relationship: v1.Relationship.create({
          resource: v1.ObjectReference.create({
            objectType: RESTAURANT,
            objectId: restaurantId,
          }),
          relation,
          subject: v1.SubjectReference.create({
            object: v1.ObjectReference.create({
              objectType: USER,
              objectId: userId,
            }),
          }),
        }),
      }),
    ],
  });
}

export interface SpiceDbRelationshipWriterOptions {
  readonly client: RelationshipsClient;
  /** Upper bound of one write; exceeding it fails closed. */
  readonly timeoutMs: number;
}

/**
 * SpiceDB writer of `restaurant#owner` and `restaurant#staff` (design of
 * `add-restaurant-delivery-relationships`): synchronous `TOUCH`/`DELETE`
 * before the business change is stored; each write returns its ZedToken
 * (`writtenAt`), stored with the restaurant. Any error or timeout throws
 * {@link AuthorizationUnavailableError}, which aborts the use case.
 */
export class SpiceDbRelationshipWriter
  implements OwnerRelationshipWriter, StaffRelationshipWriter
{
  readonly #client: RelationshipsClient;
  readonly #timeoutMs: number;

  constructor(options: SpiceDbRelationshipWriterOptions) {
    this.#client = options.client;
    this.#timeoutMs = options.timeoutMs;
  }

  touchOwner(input: {
    restaurantId: string;
    ownerId: string;
  }): Promise<AuthorizationRevision> {
    return this.#write("touch", "owner", input.restaurantId, input.ownerId);
  }

  touchStaff(input: {
    restaurantId: string;
    userId: string;
  }): Promise<AuthorizationRevision> {
    return this.#write("touch", "staff", input.restaurantId, input.userId);
  }

  deleteStaff(input: {
    restaurantId: string;
    userId: string;
  }): Promise<AuthorizationRevision> {
    return this.#write("delete", "staff", input.restaurantId, input.userId);
  }

  async #write(
    operation: RelationshipOperation,
    relation: RestaurantRelation,
    restaurantId: string,
    userId: string,
  ): Promise<AuthorizationRevision> {
    let response: v1.WriteRelationshipsResponse;
    try {
      response = await withDeadline(
        this.#client.writeRelationships(
          restaurantRelationshipWrite(
            operation,
            relation,
            restaurantId,
            userId,
          ),
        ),
        this.#timeoutMs,
        "write",
      );
    } catch (error) {
      throw new AuthorizationUnavailableError(error);
    }
    return writtenAt(response);
  }
}

/** ZedToken of a write; a response without one is a protocol error. */
export function writtenAt(
  response: v1.WriteRelationshipsResponse,
): AuthorizationRevision {
  const token = response.writtenAt?.token ?? "";
  if (token === "") {
    throw new AuthorizationUnavailableError(
      new Error("SpiceDB write returned no ZedToken"),
    );
  }
  return token;
}
