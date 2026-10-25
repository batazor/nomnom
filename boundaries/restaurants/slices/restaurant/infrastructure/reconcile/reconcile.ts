// Reconciliation of `restaurant#owner` and `restaurant#staff` with the
// Restaurants in storage (design of `add-restaurant-delivery-relationships`,
// FR-445). Port of the `orders` algorithm: graph → business by
// ReadRelationships pages, then business → graph over all restaurants. Orphan
// and stale relationships are deleted only when they stayed orphan on two
// passes at least `grace` apart, because writers TOUCH before the restaurant
// is stored; missing relationships are restored with TOUCH immediately.
// SpiceDB stays a derived graph; the restaurant storage is the source of truth.
import { v1 } from "@authzed/authzed-node";

import {
  type RelationshipsClient,
  withDeadline,
} from "../../../../infrastructure/spicedb/access-control.ts";
import {
  RESTAURANT,
  type RelationshipOperation,
  type RestaurantRelation,
  restaurantRelationshipWrite,
  USER,
} from "../../../../infrastructure/spicedb/relationships.ts";
import type { Clock } from "../../../../pkg/application/clock.ts";

/** Relationship holders of one restaurant as stored in the business state. */
export type RestaurantMembers = Readonly<{
  restaurantId: string;
  ownerId: string;
  staff: readonly string[];
}>;

/** Business side of the reconciliation. */
export interface RestaurantMemberships {
  /** Members of the existing restaurants among `ids`; missing ids are absent. */
  membersOf(
    ids: readonly string[],
  ): Promise<ReadonlyMap<string, RestaurantMembers>>;
  /** Restaurants ordered by id, strictly after `after`. */
  page(
    after: string | undefined,
    limit: number,
  ): Promise<readonly RestaurantMembers[]>;
}

/** Drift counters of one pass, summed over both relations. */
export type ReconcileStats = {
  checked: number;
  orphansPending: number;
  orphansDeleted: number;
  missingRepaired: number;
  /** Business ids SpiceDB cannot store; their writer could not write them either. */
  unrepairable: number;
};

/** SpiceDB object id grammar. */
export const OBJECT_ID = /^[a-zA-Z0-9/_|\-=+]+$/;

const RELATIONS: readonly RestaurantRelation[] = ["owner", "staff"];

const holders = (
  members: RestaurantMembers,
  relation: RestaurantRelation,
): readonly string[] =>
  relation === "owner" ? [members.ownerId] : members.staff;

export interface RelationshipReconcilerOptions {
  readonly client: RelationshipsClient;
  readonly restaurants: RestaurantMemberships;
  /** Minimum time a relationship stays orphan before it is deleted. */
  readonly graceMs: number;
  /** ReadRelationships page and business page size. */
  readonly pageSize: number;
  /** Deadline of one SpiceDB call. */
  readonly timeoutMs: number;
  readonly clock: Clock;
  /**
   * Restricts both sides to restaurant ids with this prefix. Production
   * leaves it empty; tests against a shared SpiceDB scope themselves with it.
   */
  readonly resourceIdPrefix?: string;
  readonly log?: (message: string) => void;
}

/**
 * Remembers orphan candidates between passes in memory: a restaurant's
 * storage lives in the same process, so a restart forgets both.
 */
export class RelationshipReconciler {
  readonly #client: RelationshipsClient;
  readonly #restaurants: RestaurantMemberships;
  readonly #graceMs: number;
  readonly #pageSize: number;
  readonly #timeoutMs: number;
  readonly #clock: Clock;
  readonly #prefix: string;
  readonly #log: (message: string) => void;
  /** `relation:restaurantId:userId` → first time seen orphan (ms). */
  readonly #candidates = new Map<string, number>();

  constructor(options: RelationshipReconcilerOptions) {
    this.#client = options.client;
    this.#restaurants = options.restaurants;
    this.#graceMs = options.graceMs;
    this.#pageSize = options.pageSize;
    this.#timeoutMs = options.timeoutMs;
    this.#clock = options.clock;
    this.#prefix = options.resourceIdPrefix ?? "";
    this.#log = options.log ?? console.log;
  }

  /** Orphan candidates waiting for their grace period; for tests and logs. */
  get pendingCandidates(): number {
    return this.#candidates.size;
  }

  /**
   * Runs one pass over both relations. `signal` stops it between SpiceDB
   * calls (shutdown); an error ends the pass and is rethrown.
   */
  async reconcile(signal?: AbortSignal): Promise<ReconcileStats> {
    const stats: ReconcileStats = {
      checked: 0,
      orphansPending: 0,
      orphansDeleted: 0,
      missingRepaired: 0,
      unrepairable: 0,
    };
    const orphans = new Set<string>();
    for (const relation of RELATIONS) {
      const present = await this.#graphToBusiness(
        relation,
        stats,
        orphans,
        signal,
      );
      await this.#businessToGraph(relation, present, stats, signal);
    }
    // A candidate that is no longer orphan (its restaurant was stored) is forgotten.
    for (const key of this.#candidates.keys()) {
      if (!orphans.has(key)) this.#candidates.delete(key);
    }
    this.#log(
      `reconcile restaurant#owner,staff: checked=${stats.checked} orphans_pending=${stats.orphansPending} orphans_deleted=${stats.orphansDeleted} missing_repaired=${stats.missingRepaired} unrepairable=${stats.unrepairable}`,
    );
    return stats;
  }

  async #graphToBusiness(
    relation: RestaurantRelation,
    stats: ReconcileStats,
    orphans: Set<string>,
    signal: AbortSignal | undefined,
  ): Promise<Set<string>> {
    const present = new Set<string>();
    let cursor: v1.Cursor | undefined;
    for (;;) {
      signal?.throwIfAborted();
      const page = await this.#readPage(relation, cursor);
      if (page.length === 0) break;
      const members = await this.#restaurants.membersOf(
        page.map((response) => response.relationship?.resource?.objectId ?? ""),
      );
      for (const response of page) {
        const restaurantId = response.relationship?.resource?.objectId ?? "";
        const userId = response.relationship?.subject?.object?.objectId ?? "";
        stats.checked += 1;
        const pair = `${restaurantId}:${userId}`;
        present.add(pair);
        const stored = members.get(restaurantId);
        if (
          stored !== undefined &&
          holders(stored, relation).includes(userId)
        ) {
          continue;
        }
        const key = `${relation}:${pair}`;
        orphans.add(key);
        const now = this.#clock.now().getTime();
        const first = this.#candidates.get(key);
        if (first === undefined) {
          this.#candidates.set(key, now);
          stats.orphansPending += 1;
          continue;
        }
        if (now - first < this.#graceMs) {
          stats.orphansPending += 1;
          continue;
        }
        signal?.throwIfAborted();
        await this.#write("delete", relation, restaurantId, userId);
        this.#candidates.delete(key);
        stats.orphansDeleted += 1;
      }
      const next = page.at(-1)?.afterResultCursor;
      if (next === undefined || page.length < this.#pageSize) break;
      cursor = next;
    }
    return present;
  }

  async #businessToGraph(
    relation: RestaurantRelation,
    present: ReadonlySet<string>,
    stats: ReconcileStats,
    signal: AbortSignal | undefined,
  ): Promise<void> {
    let after: string | undefined;
    for (;;) {
      signal?.throwIfAborted();
      const page = await this.#restaurants.page(after, this.#pageSize);
      for (const members of page) {
        if (!members.restaurantId.startsWith(this.#prefix)) continue;
        for (const userId of holders(members, relation)) {
          if (present.has(`${members.restaurantId}:${userId}`)) continue;
          if (
            !OBJECT_ID.test(members.restaurantId) ||
            !OBJECT_ID.test(userId)
          ) {
            this.#log(
              `reconcile: restaurant ${JSON.stringify(members.restaurantId)} ${relation} ${JSON.stringify(userId)} has an id SpiceDB cannot store`,
            );
            stats.unrepairable += 1;
            continue;
          }
          signal?.throwIfAborted();
          await this.#write("touch", relation, members.restaurantId, userId);
          stats.missingRepaired += 1;
        }
      }
      if (page.length < this.#pageSize) return;
      after = page.at(-1)?.restaurantId;
    }
  }

  async #readPage(
    relation: RestaurantRelation,
    cursor: v1.Cursor | undefined,
  ): Promise<v1.ReadRelationshipsResponse[]> {
    const request = v1.ReadRelationshipsRequest.create({
      consistency: v1.Consistency.create({
        requirement: { oneofKind: "fullyConsistent", fullyConsistent: true },
      }),
      relationshipFilter: v1.RelationshipFilter.create({
        resourceType: RESTAURANT,
        optionalResourceIdPrefix: this.#prefix,
        optionalRelation: relation,
        optionalSubjectFilter: v1.SubjectFilter.create({
          subjectType: USER,
        }),
      }),
      optionalLimit: this.#pageSize,
      optionalCursor: cursor,
    });
    try {
      return await withDeadline(
        this.#client.readRelationships(request),
        this.#timeoutMs,
        "read",
      );
    } catch (error) {
      throw new Error(`read restaurant#${relation} relationships`, {
        cause: error,
      });
    }
  }

  async #write(
    operation: RelationshipOperation,
    relation: RestaurantRelation,
    restaurantId: string,
    userId: string,
  ): Promise<void> {
    try {
      await withDeadline(
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
      throw new Error(
        `${operation} restaurant:${restaurantId}#${relation}@user:${userId}`,
        { cause: error },
      );
    }
  }
}
