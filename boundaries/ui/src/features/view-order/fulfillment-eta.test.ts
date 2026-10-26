import { describe, expect, it } from "vitest";

import type { OrderMilestoneKind } from "../../shared/graphql/generated/graphql";
import {
  activeStepEta,
  ariaPercent,
  countdownText,
  deadlineShiftMinutes,
  etaPercent,
  etaRemaining,
  headerEta,
} from "./fulfillment-eta";
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
    placedAt: "2026-10-25T12:00:00Z",
    milestones: [
      { kind: "PLACED", occurredAt: "2026-10-25T12:00:00Z" },
      ...milestones.map(([kind, occurredAt]) => ({ kind, occurredAt })),
    ],
    cancellation: null,
    expectedReadyAt: null,
    expectedPickupAt: null,
    expectedDeliveredAt: null,
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

  it.each(["RECEIVED", "DELIVERED", "CANCELLED", "FAILED"] as const)(
    "has no bar for a %s order",
    (status) => {
      expect(
        activeStepEta(order(status, [accepted, assigned], allDeadlines)),
      ).toBeNull();
    },
  );
});

describe("eta formatting", () => {
  const minute = 60_000;

  it("rounds minutes up above five minutes", () => {
    expect(etaRemaining(12 * minute, 0)).toEqual({
      kind: "minutes",
      minutes: 12,
    });
    expect(etaRemaining(5 * minute + 1, 0)).toEqual({
      kind: "minutes",
      minutes: 6,
    });
  });

  it("counts down m:ss from five minutes", () => {
    const five = etaRemaining(5 * minute, 0);
    expect(five).toEqual({ kind: "countdown", minutes: 5, seconds: 0 });
    const short = etaRemaining(4 * minute + 59_400, 0);
    expect(short.kind === "countdown" && countdownText(short)).toBe("5:00");
    const last = etaRemaining(9_000, 0);
    expect(last.kind === "countdown" && countdownText(last)).toBe("0:09");
  });

  it("reports a missed deadline in whole minutes of delay", () => {
    expect(etaRemaining(0, 0)).toEqual({ kind: "overdue", minutes: 1 });
    expect(etaRemaining(0, 2 * minute + 1)).toEqual({
      kind: "overdue",
      minutes: 3,
    });
  });

  it("measures progress and rounds aria-valuenow to 5%", () => {
    const eta = {
      stage: "cooking",
      label: "order.eta.cooking",
      startedAt: 0,
      deadline: 10 * minute,
    } as const;
    expect(etaPercent(eta, 0)).toBe(0);
    expect(etaPercent(eta, 3.3 * minute)).toBeCloseTo(33);
    expect(etaPercent(eta, 11 * minute)).toBe(100);
    expect(ariaPercent(33)).toBe(35);
    expect(ariaPercent(32.4)).toBe(30);
    expect(ariaPercent(100)).toBe(100);
  });

  it("names only a later deadline as a shift", () => {
    expect(deadlineShiftMinutes(0, 6 * minute)).toBe(6);
    expect(deadlineShiftMinutes(6 * minute, 0)).toBeNull();
    expect(deadlineShiftMinutes(0, 10_000)).toBeNull();
  });
});
