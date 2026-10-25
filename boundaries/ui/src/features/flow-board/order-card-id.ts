// The cards the order details grow out of.
export type DetailsSource = "kitchen" | "courier" | "timeline";

// One id per place an order card shows up, so the details fold back into the
// card they came from.
export function orderCardId(from: DetailsSource, orderId: string): string {
  return `order-${from}-${orderId}`;
}
