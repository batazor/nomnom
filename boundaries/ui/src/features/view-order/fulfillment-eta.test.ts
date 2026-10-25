import { describe, expect, it } from "vitest";

import type { OrderMilestoneKind } from "../../shared/graphql/generated/graphql";
import { etaBars, etaProgress, runsClock } from "./fulfillment-eta";
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

describe("fulfillment eta", () => {
  it("counts cooking from the kitchen acceptance to expectedReadyAt", () => {
    const bars = etaBars(
      order("CONFIRMED", [accepted], {
        expectedReadyAt: "2026-10-25T12:11:00Z",
      }),
    );
    expect(bars).toEqual([
      {
        stage: "cooking",
        kind: "progress",
        label: "order.eta.cooking",
        startedAt: Date.parse("2026-10-25T12:01:00Z"),
        deadline: Date.parse("2026-10-25T12:11:00Z"),
      },
    ]);
    expect(runsClock(bars)).toBe(true);
  });

  it("shows no bar without a start moment, even with a deadline", () => {
    expect(
      etaBars(
        order("CONFIRMED", [], { expectedReadyAt: "2026-10-25T12:11:00Z" }),
      ),
    ).toEqual([]);
    expect(etaBars(order("RECEIVED", []))).toEqual([]);
  });

  it("shows an empty state instead of a bar until the deadline is named", () => {
    const bars = etaBars(order("PREPARING", [accepted, assigned]));
    expect(bars.map((bar) => [bar.stage, bar.kind, bar.label])).toEqual([
      ["cooking", "unnamed", "order.eta.cooking"],
      ["delivery", "unnamed", "order.eta.toPickup"],
    ]);
    expect(runsClock(bars)).toBe(false);
  });

  it("heads to the pickup before it and to the delivery after it", () => {
    const deadlines = {
      expectedReadyAt: "2026-10-25T12:11:00Z",
      expectedPickupAt: "2026-10-25T12:15:00Z",
      expectedDeliveredAt: "2026-10-25T12:30:00Z",
    };
    const before = etaBars(order("PREPARING", [accepted, assigned], deadlines));
    expect(before[1]).toMatchObject({
      label: "order.eta.toPickup",
      deadline: Date.parse(deadlines.expectedPickupAt),
    });

    const after = etaBars(
      order(
        "PICKED_UP",
        [
          accepted,
          assigned,
          ["READY_FOR_PICKUP", "2026-10-25T12:12:00Z"],
          ["PICKED_UP", "2026-10-25T12:16:00Z"],
        ],
        deadlines,
      ),
    );
    expect(after).toEqual([
      {
        stage: "cooking",
        kind: "done",
        label: "order.eta.cooking",
        finishedAt: "2026-10-25T12:12:00Z",
      },
      {
        stage: "delivery",
        kind: "progress",
        label: "order.eta.delivery",
        startedAt: Date.parse("2026-10-25T12:05:00Z"),
        deadline: Date.parse(deadlines.expectedDeliveredAt),
      },
    ]);
  });

  it("counts from the latest assignment of a reassigned courier", () => {
    const [, delivery] = etaBars(
      order(
        "PREPARING",
        [accepted, assigned, ["COURIER_ASSIGNED", "2026-10-25T12:08:00Z"]],
        { expectedPickupAt: "2026-10-25T12:20:00Z" },
      ),
    );
    expect(delivery).toMatchObject({
      startedAt: Date.parse("2026-10-25T12:08:00Z"),
    });
  });

  it.each(["DELIVERED", "CANCELLED", "FAILED"] as const)(
    "shows no bars for a %s order",
    (status) => {
      expect(
        etaBars(
          order(status, [accepted, assigned], {
            expectedReadyAt: "2026-10-25T12:11:00Z",
          }),
        ),
      ).toEqual([]);
    },
  );

  it("reports the time left and a missed deadline as delayed", () => {
    const bar = {
      stage: "cooking",
      kind: "progress",
      label: "order.eta.cooking",
      startedAt: 0,
      deadline: 600_000,
    } as const;
    expect(etaProgress(bar, 0)).toEqual({
      percent: 0,
      remaining: { kind: "minutes", minutes: 10 },
    });
    expect(etaProgress(bar, 330_000)).toEqual({
      percent: 55,
      remaining: { kind: "minutes", minutes: 5 },
    });
    expect(etaProgress(bar, 570_000)).toEqual({
      percent: 95,
      remaining: { kind: "lessThanMinute" },
    });
    expect(etaProgress(bar, 600_000)).toEqual({
      percent: 100,
      remaining: { kind: "delayed" },
    });
  });
});
