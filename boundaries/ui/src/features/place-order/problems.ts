import type { PlaceOrderProblemCode } from "../../shared/graphql/generated/graphql";
import type { MessageKey } from "../../shared/i18n/messages";

// Message for each affected cart line, by menu item id.
export type ItemProblems = ReadonlyMap<string, MessageKey>;

const orderProblemMessage: Record<PlaceOrderProblemCode, MessageKey> = {
  ORDER_ITEM_UNAVAILABLE: "checkout.problem.ORDER_ITEM_UNAVAILABLE",
  ORDER_TOTAL_BELOW_MINIMUM: "checkout.problem.ORDER_TOTAL_BELOW_MINIMUM",
  ORDER_TOTAL_ABOVE_MAXIMUM: "checkout.problem.ORDER_TOTAL_ABOVE_MAXIMUM",
  RESTAURANT_NOT_ACCEPTING_ORDERS:
    "checkout.problem.RESTAURANT_NOT_ACCEPTING_ORDERS",
  PREMIUM_UNAVAILABLE: "checkout.problem.PREMIUM_UNAVAILABLE",
};

const itemProblemMessage: Record<PlaceOrderProblemCode, MessageKey> = {
  ORDER_ITEM_UNAVAILABLE: "checkout.itemProblem.unavailable",
  ORDER_TOTAL_BELOW_MINIMUM: "checkout.itemProblem.other",
  ORDER_TOTAL_ABOVE_MAXIMUM: "checkout.itemProblem.other",
  RESTAURANT_NOT_ACCEPTING_ORDERS: "checkout.itemProblem.other",
  PREMIUM_UNAVAILABLE: "checkout.itemProblem.other",
};

// A problem with menuItemIds belongs next to those cart lines; one without
// them refers to the whole order and is shown above the form.
export function splitProblems(
  problems: readonly {
    code: PlaceOrderProblemCode;
    menuItemIds: readonly string[];
  }[],
): { orderProblems: MessageKey[]; itemProblems: ItemProblems } {
  const orderProblems = new Set<MessageKey>();
  const itemProblems = new Map<string, MessageKey>();
  for (const problem of problems) {
    if (problem.menuItemIds.length === 0) {
      orderProblems.add(orderProblemMessage[problem.code]);
      continue;
    }
    for (const id of problem.menuItemIds) {
      if (!itemProblems.has(id)) {
        itemProblems.set(id, itemProblemMessage[problem.code]);
      }
    }
  }
  return { orderProblems: [...orderProblems], itemProblems };
}
