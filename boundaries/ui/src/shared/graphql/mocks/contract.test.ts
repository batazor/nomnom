import {
  execute,
  type FragmentDefinitionNode,
  type GraphQLObjectType,
  Kind,
  type OperationDefinitionNode,
  parse,
  print,
  subscribe,
  validate,
} from "graphql";
import { describe, expect, it } from "vitest";

import { schemaMismatches } from "./conformance";
import { trackingChannel } from "./courier-tracks";
import {
  createOrderScenarios,
  kitchenEight,
  liveOrderAt,
  liveOrderStepCount,
  nightBakery,
  restaurantFixtures,
} from "./fixtures";
import { mockCourierId } from "./fulfillment";
import { publicSchema } from "./schema";
import { createMockStore } from "./store";

const documents = {
  ...import.meta.glob<string>("/src/features/**/*.graphql", {
    query: "?raw",
    import: "default",
    eager: true,
  }),
  ...import.meta.glob<string>("./operations/*.graphql", {
    query: "?raw",
    import: "default",
    eager: true,
  }),
};

// Variables for every operation the UI sends; a new operation must be added here.
const variablesByOperation: Record<string, Record<string, unknown>> = {
  BffInfo: {},
  ViewOrder: { id: "order-preparing" },
  OrderChanged: { id: "order-preparing" },
  CourierMoved: { channel: trackingChannel("order-preparing") },
  RequestOrderCancellation: {
    input: { orderId: "order-received", requestId: "contract-cancel" },
  },
  BrowseRestaurants: { first: 2 },
  ViewRestaurant: { id: kitchenEight.id },
  MockCustomerOrders: { first: 3 },
  OpenRestaurant: { restaurantId: nightBakery.id },
  CloseRestaurant: { restaurantId: kitchenEight.id },
  BoardKitchen: { restaurantId: kitchenEight.id },
  BoardOwnedRestaurants: {},
  ApplyKitchenAction: {
    input: {
      restaurantId: kitchenEight.id,
      kitchenOrderId: "kitchen-order-paid",
      action: "ACCEPT",
    },
  },
  StartNextPreparation: {
    restaurantId: kitchenEight.id,
    requestId: "attempt-1",
  },
  BoardCourier: {},
  DemoSimulator: {},
  SetSimulatorEnabled: { enabled: true },
  SetCourierAvailability: { availability: "AVAILABLE" },
  AcceptDeliveryOffer: { offerId: "offer-order-preparing" },
  ApplyCourierDeliveryAction: {
    deliveryId: "delivery-order-preparing",
    action: "CONFIRM_PICKUP",
  },
  BoardOrderStatus: { id: "order-preparing" },
  BoardOrderChanged: { id: "order-preparing" },
  BoardKitchenChanged: { restaurantId: kitchenEight.id },
  BoardCourierChanged: { id: mockCourierId },
  HireCook: { restaurantId: kitchenEight.id, name: "Тимур" },
  SetCookOnLine: {
    restaurantId: kitchenEight.id,
    cookId: `cook-${kitchenEight.id}-4`,
    onLine: true,
  },
  RetireCook: {
    restaurantId: kitchenEight.id,
    cookId: `cook-${kitchenEight.id}-4`,
  },
  BoardCourierPool: {},
  BoardCourierPoolChanged: { marketId: "berlin" },
  AddCourier: { name: "Сергей" },
  SetCourierOnLine: { courierId: "courier-anna", onLine: true },
  RetireCourier: { courierId: "courier-anna" },
  PlaceOrder: {
    input: {
      restaurantId: kitchenEight.id,
      items: [{ menuItemId: "menu-bao", quantity: 1 }],
      idempotencyKey: "contract-place",
      priority: "STANDARD",
    },
  },
};

// Fragments an operation spreads, also through other fragments.
function usedFragments(
  operation: OperationDefinitionNode,
  fragments: readonly FragmentDefinitionNode[],
): FragmentDefinitionNode[] {
  const used: FragmentDefinitionNode[] = [];
  let sources = print(operation);
  for (let grown = true; grown; ) {
    grown = false;
    for (const fragment of fragments) {
      if (used.includes(fragment)) continue;
      if (!sources.includes(`...${fragment.name.value}`)) continue;
      used.push(fragment);
      sources += print(fragment);
      grown = true;
    }
  }
  return used;
}

// Each operation goes with the fragments of its file, as codegen sends it.
const operations = Object.entries(documents).flatMap(([file, source]) => {
  const { definitions } = parse(source);
  const fragments = definitions.filter(
    (definition): definition is FragmentDefinitionNode =>
      definition.kind === Kind.FRAGMENT_DEFINITION,
  );
  return definitions
    .filter(
      (definition): definition is OperationDefinitionNode =>
        definition.kind === Kind.OPERATION_DEFINITION,
    )
    .map((definition) => ({
      file,
      name: definition.name?.value ?? "",
      kind: definition.operation,
      document: {
        kind: Kind.DOCUMENT,
        definitions: [definition, ...usedFragments(definition, fragments)],
      } as const,
    }));
});

function schemaType(name: string): GraphQLObjectType {
  return publicSchema.getType(name) as GraphQLObjectType;
}

describe("mock contract", () => {
  it("covers every customer operation of the schema", () => {
    const rootFields = [
      ...Object.keys(publicSchema.getQueryType()?.getFields() ?? {}),
      ...Object.keys(publicSchema.getMutationType()?.getFields() ?? {}),
      ...Object.keys(publicSchema.getSubscriptionType()?.getFields() ?? {}),
    ];
    const store = createMockStore();
    expect(Object.keys(store.rootValue).sort()).toEqual(rootFields.sort());
  });

  it.each(operations)(
    "$name from $file conforms to the schema",
    async ({ name, kind, document }) => {
      expect(validate(publicSchema, document)).toEqual([]);
      const variables = variablesByOperation[name];
      expect(variables, `variables for ${name}`).toBeDefined();

      const args = {
        schema: publicSchema,
        document,
        rootValue: createMockStore().rootValue,
        variableValues: variables,
      };
      if (kind === "subscription") {
        // A stream yields only after a change; the store test follows one.
        const stream = await subscribe(args);
        expect(Symbol.asyncIterator in stream).toBe(true);
        if (Symbol.asyncIterator in stream) await stream.return?.();
        return;
      }
      const result = await execute(args);
      expect(result.errors).toBeUndefined();
    },
  );

  it("keeps every fixture inside the schema", () => {
    const now = new Date("2026-10-25T18:00:00Z");
    const orders = [
      ...createOrderScenarios(now),
      ...Array.from({ length: liveOrderStepCount }, (_, step) =>
        liveOrderAt(step, now),
      ),
    ];
    const mismatches = [
      ...restaurantFixtures.flatMap((r) =>
        schemaMismatches(r, schemaType("Restaurant")),
      ),
      ...orders.flatMap((o) => schemaMismatches(o, schemaType("Order"), o.id)),
    ];
    expect(mismatches).toEqual([]);
  });

  it("rejects a fixture with a field or enum value outside the schema", () => {
    const [order] = createOrderScenarios(new Date());
    expect(
      schemaMismatches(
        { ...order, courierName: "Алексей" },
        schemaType("Order"),
      ),
    ).toEqual(["$.courierName: not a field of Order"]);
    expect(
      schemaMismatches({ ...order, status: "COOKING" }, schemaType("Order")),
    ).toEqual(["$.status: COOKING is not a OrderStatus value"]);
    expect(
      schemaMismatches(
        { ...order, total: { amount: "1.00" } },
        schemaType("Order"),
      ),
    ).toEqual(["$.total.currency: required"]);
  });
});
