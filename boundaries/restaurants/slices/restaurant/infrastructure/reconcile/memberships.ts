/** Relationship holders of one restaurant as stored in the business state. */
export type RestaurantMembers = Readonly<{
  restaurantId: string;
  /**
   * Version of the stored Restaurant document. A missing relationship is
   * restored only when two passes grace apart saw the same version (FR-445).
   */
  version: number;
  ownerId: string;
  staff: readonly string[];
}>;

/** Version change of a Restaurant document by the reconciler's revision write. */
export type RevisionWrite = Readonly<{ from: number; to: number }>;

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
   * its next check sees the restored relationship (FR-447). Returns the
   * document version it overwrote and the version it wrote; `undefined` when
   * the restaurant no longer exists and was skipped.
   */
  recordAuthorizationRevision(
    restaurantId: string,
    revision: string,
  ): Promise<RevisionWrite | undefined>;
}

/** Drift counters of one pass, summed over both relations. */
export type ReconcileStats = {
  checked: number;
  orphansPending: number;
  orphansDeleted: number;
  /** Missing relationships seen on fewer than two passes grace apart. */
  missingPending: number;
  missingRepaired: number;
  /** Business ids SpiceDB cannot store; their writer could not write them either. */
  unrepairable: number;
};
