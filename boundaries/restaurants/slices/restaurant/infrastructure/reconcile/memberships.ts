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
  /**
   * Stores the revision (ZedToken) of a repairing TOUCH on the restaurant, so
   * its next check sees the restored relationship (FR-447). A restaurant
   * that no longer exists is skipped.
   */
  recordAuthorizationRevision(
    restaurantId: string,
    revision: string,
  ): Promise<void>;
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
