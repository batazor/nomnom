export const MenuItemAvailabilityReason = {
  OutsideAvailabilityWindow: "outside_availability_window",
  TemporarilyUnavailable: "temporarily_unavailable",
  InsufficientCapacity: "insufficient_capacity",
} as const;

export type MenuItemAvailabilityReason =
  (typeof MenuItemAvailabilityReason)[keyof typeof MenuItemAvailabilityReason];

export type MenuItemAvailabilityContext = Readonly<{
  now: Date;
  availableFrom: Date;
  availableUntil: Date;
  requestedQuantity: number;
  remainingCapacity: number;
  temporarilyUnavailable: boolean;
}>;

export type MenuItemAvailabilityResult = Readonly<{
  satisfied: boolean;
  reasons: readonly MenuItemAvailabilityReason[];
}>;

export const MenuItemAvailable = (
  context: MenuItemAvailabilityContext,
): MenuItemAvailabilityResult => {
  const reasons: MenuItemAvailabilityReason[] = [];
  const now = context.now.getTime();
  if (
    now < context.availableFrom.getTime() ||
    now > context.availableUntil.getTime()
  ) {
    reasons.push(MenuItemAvailabilityReason.OutsideAvailabilityWindow);
  }
  if (context.temporarilyUnavailable) {
    reasons.push(MenuItemAvailabilityReason.TemporarilyUnavailable);
  }
  if (context.requestedQuantity > context.remainingCapacity) {
    reasons.push(MenuItemAvailabilityReason.InsufficientCapacity);
  }
  return { satisfied: reasons.length === 0, reasons };
};
