export const InvalidRestaurantReason = {
  EmptyName: "empty_name",
  EmptyOwner: "empty_owner",
  EmptyStaff: "empty_staff",
} as const;

export type InvalidRestaurantReason =
  (typeof InvalidRestaurantReason)[keyof typeof InvalidRestaurantReason];

export class InvalidRestaurantError extends Error {
  readonly code = "invalid_restaurant";
  readonly reason: InvalidRestaurantReason;

  constructor(reason: InvalidRestaurantReason) {
    super(`invalid restaurant: ${reason}`);
    this.name = "InvalidRestaurantError";
    this.reason = reason;
  }
}
