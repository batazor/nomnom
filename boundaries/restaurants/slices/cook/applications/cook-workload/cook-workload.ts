/**
 * Work of the kitchen the Cook Pool depends on, read from the Restaurant
 * Orders of the `restaurantorder` slice: a cook is busy while an order with
 * its id is in Active Preparation. Reads join the active Unit of Work.
 */
export interface CookWorkload {
  /** Cook id → id of the Restaurant Order the cook holds. */
  activeAssignments(restaurantId: string): Promise<ReadonlyMap<string, string>>;
  /** Restaurant Orders in `QUEUED`: the backlog of the pool. */
  queuedOrders(restaurantId: string): Promise<number>;
}

/** Whether the restaurant exists, read from the `restaurant` slice. */
export interface CookRestaurants {
  exists(restaurantId: string): Promise<boolean>;
}
