export const RestaurantAcceptanceReason = {
  OutsideOpeningHours: "outside_opening_hours",
  ScheduleException: "schedule_exception",
  CapacityExhausted: "capacity_exhausted",
} as const;

export type RestaurantAcceptanceReason =
  (typeof RestaurantAcceptanceReason)[keyof typeof RestaurantAcceptanceReason];

export type RestaurantAcceptanceContext = Readonly<{
  now: Date;
  opensAt: Date;
  closesAt: Date;
  currentOrders: number;
  maximumOrders: number;
  scheduleException: boolean;
}>;

export type RestaurantAcceptanceResult = Readonly<{
  satisfied: boolean;
  reasons: readonly RestaurantAcceptanceReason[];
}>;

export const RestaurantAcceptingOrders = (
  context: RestaurantAcceptanceContext,
): RestaurantAcceptanceResult => {
  const reasons: RestaurantAcceptanceReason[] = [];
  const now = context.now.getTime();
  if (now < context.opensAt.getTime() || now >= context.closesAt.getTime()) {
    reasons.push(RestaurantAcceptanceReason.OutsideOpeningHours);
  }
  if (context.scheduleException) {
    reasons.push(RestaurantAcceptanceReason.ScheduleException);
  }
  if (context.currentOrders >= context.maximumOrders) {
    reasons.push(RestaurantAcceptanceReason.CapacityExhausted);
  }
  return { satisfied: reasons.length === 0, reasons };
};
