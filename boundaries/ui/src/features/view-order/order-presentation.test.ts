import type { GraphQLEnumType } from "graphql";
import { describe, expect, it } from "vitest";

import { publicSchema } from "../../shared/graphql/mocks/schema";
import { messages } from "../../shared/i18n/messages";
import {
  endPresentation,
  endReasonPresentation,
  milestoneLabel,
  showsCourierAssigned,
  statusPresentation,
} from "./order-presentation";

function enumValues(name: string): string[] {
  const type = publicSchema.getType(name) as GraphQLEnumType;
  return type.getValues().map((value) => value.name);
}

describe("order presentation", () => {
  it.each([
    ["OrderStatus", statusPresentation],
    ["OrderEndReason", endReasonPresentation],
    ["OrderMilestoneKind", milestoneLabel],
  ] as const)("maps every %s value of the public schema", (name, mapping) => {
    expect(Object.keys(mapping).sort()).toEqual(enumValues(name).sort());
  });

  it("uses only message keys that exist in every locale", () => {
    const keys = [
      ...Object.values(statusPresentation).flatMap((p) => [p.label, p.detail]),
      ...Object.values(endReasonPresentation).flatMap((p) => [
        p.title,
        p.detail,
      ]),
      ...Object.values(milestoneLabel),
    ];
    for (const locale of Object.values(messages)) {
      expect(keys.filter((key) => !(key in locale))).toEqual([]);
    }
  });

  it("presents a restaurant rejection with its own message", () => {
    expect(endPresentation("RESTAURANT_REJECTED").title).toBe(
      "order.end.RESTAURANT_REJECTED.title",
    );
    expect(endPresentation(null)).toBe(endReasonPresentation.NOT_FULFILLED);
  });

  it("notes the courier while the kitchen still works on the order", () => {
    const assigned = [
      { kind: "PLACED" as const },
      { kind: "COURIER_ASSIGNED" as const },
    ];
    expect(showsCourierAssigned("PREPARING", assigned)).toBe(true);
    expect(showsCourierAssigned("PREPARING", assigned.slice(0, 1))).toBe(false);
    expect(showsCourierAssigned("PICKED_UP", assigned)).toBe(false);
  });
});
