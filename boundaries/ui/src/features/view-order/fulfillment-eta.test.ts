import { describe, expect, it } from "vitest";

import type { OrderMilestoneKind } from "../../shared/graphql/generated/graphql";
import { activeStepEta, headerEta } from "./fulfillment-eta";
import type { ViewedOrder } from "./use-order-updates";

const money = { amount: "100.00", currency: "RUB" };

function order(
  status: ViewedOrder["status"],
  milestones: ReadonlyArray<readonly [OrderMilestoneKind, string]>,
  deadlines: Partial<
    Pick<
      ViewedOrder,
      "expectedReadyAt" | "expectedPickupAt" | "expectedDeliveredAt"
    >
  > = {},
): ViewedOrder {
  return {
    id: "order-1",
    status,
    endReason: null,
    restaurant: { id: "restaurant-1", name: "Кухня №8" },
    items: [],
    total: money,
    priority: "STANDARD",
    premiumFee: null,
    placedAt: "2026-10-25T12:00:00Z",
    deliveryWindow: null,
    milestones: [
      { kind: "PLACED", occurredAt: "2026-10-25T12:00:00Z" },
      ...milestones.map(([kind, occurredAt]) => ({ kind, occurredAt })),
    ],
    cancellation: null,
    expectedReadyAt: null,
    expectedPickupAt: null,
    expectedDeliveredAt: null,
    courier: null,
    tracking: null,
    ...deadlines,
  };
}

const accepted = ["CONFIRMED", "2026-10-25T12:01:00Z"] as const;
const assigned = ["COURIER_ASSIGNED", "2026-10-25T12:05:00Z"] as const;

const ready = "2026-10-25T12:11:00Z";
const pickup = "2026-10-25T12:15:00Z";
const delivered = "2026-10-25T12:30:00Z";
const allDeadlines = {
  expectedReadyAt: ready,
  expectedPickupAt: pickup,
  expectedDeliveredAt: delivered,
};

describe("header deadline", () => {
  it("prefers delivery, then pickup, then readiness, without summing", () => {
    expect(headerEta(order("PICKED_UP", [accepted], allDeadlines))).toEqual({
      kind: "delivered",
      deadline: Date.parse(delivered),
    });
    expect(
      headerEta(
        order("PREPARING", [accepted, assigned], {
          expectedReadyAt: ready,
          expectedPickupAt: pickup,
        }),
      ),
    ).toEqual({ kind: "pickup", deadline: Date.parse(pickup) });
    expect(
      headerEta(order("CONFIRMED", [accepted], { expectedReadyAt: ready })),
    ).toEqual({ kind: "ready", deadline: Date.parse(ready) });
  });

  it("has no line without a deadline", () => {
    expect(headerEta(order("RECEIVED", []))).toBeNull();
    expect(headerEta(order("PREPARING", [accepted, assigned]))).toBeNull();
  });

  it.each(["DELIVERED", "CANCELLED", "FAILED"] as const)(
    "has no line for a %s order",
    (status) => {
      expect(headerEta(order(status, [accepted], allDeadlines))).toBeNull();
    },
  );
});

describe("active step deadline", () => {
  it("counts cooking from the latest kitchen acceptance to expectedReadyAt", () => {
    for (const status of ["CONFIRMED", "PREPARING"] as const) {
      expect(
        activeStepEta(
          order(
            status,
            [accepted, ["CONFIRMED", "2026-10-25T12:02:00Z"], assigned],
            allDeadlines,
          ),
        ),
      ).toEqual({
        stage: "cooking",
        label: "order.eta.cooking",
        startedAt: Date.parse("2026-10-25T12:02:00Z"),
        deadline: Date.parse(ready),
      });
    }
  });

  it("heads the courier to the pickup from the latest assignment", () => {
    expect(
      activeStepEta(
        order(
          "READY_FOR_PICKUP",
          [accepted, assigned, ["COURIER_ASSIGNED", "2026-10-25T12:08:00Z"]],
          allDeadlines,
        ),
      ),
    ).toEqual({
      stage: "toPickup",
      label: "order.eta.toPickup",
      startedAt: Date.parse("2026-10-25T12:08:00Z"),
      deadline: Date.parse(pickup),
    });
  });

  it("counts the delivery from the pickup to expectedDeliveredAt", () => {
    expect(
      activeStepEta(
        order(
          "PICKED_UP",
          [accepted, assigned, ["PICKED_UP", "2026-10-25T12:16:00Z"]],
          allDeadlines,
        ),
      ),
    ).toEqual({
      stage: "delivery",
      label: "order.eta.delivery",
      startedAt: Date.parse("2026-10-25T12:16:00Z"),
      deadline: Date.parse(delivered),
    });
  });

  it("has no bar without a start or a deadline", () => {
    expect(
      activeStepEta(order("CONFIRMED", [], { expectedReadyAt: ready })),
    ).toBeNull();
    expect(activeStepEta(order("PREPARING", [accepted]))).toBeNull();
    expect(
      activeStepEta(order("PICKED_UP", [accepted, assigned], allDeadlines)),
    ).toBeNull();
  });

  // AC-931: a scheduled order the kitchen has not started shows no cooking
  // bar and no header deadline, though the kitchen named its ready time.
  it.each([
    "RECEIVED",
    "SCHEDULED",
    "DELIVERED",
    "CANCELLED",
    "FAILED",
  ] as const)("has no bar for a %s order", (status) => {
    expect(
      activeStepEta(order(status, [accepted, assigned], allDeadlines)),
    ).toBeNull();
  });
});

describe("scheduled order", () => {
  it("has no header deadline before the kitchen starts it", () => {
    expect(
      headerEta(order("SCHEDULED", [accepted], { expectedReadyAt: ready })),
    ).toBeNull();
  });
});
