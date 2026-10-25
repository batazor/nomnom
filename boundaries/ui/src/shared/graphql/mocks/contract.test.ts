import {
  execute,
  type GraphQLObjectType,
  Kind,
  type OperationDefinitionNode,
  parse,
  validate,
} from "graphql";
import { describe, expect, it } from "vitest";

import { schemaMismatches } from "./conformance";
import {
  createOrderScenarios,
  kitchenEight,
  liveOrderAt,
  liveOrderStepCount,
  nightBakery,
  restaurantFixtures,
} from "./fixtures";
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
  RequestOrderCancellation: {
    input: { orderId: "order-received", requestId: "contract-cancel" },
  },
  BrowseRestaurants: { first: 2 },
  ViewRestaurant: { id: kitchenEight.id },
  MockCustomerOrders: { first: 3 },
  ManageRestaurant: { id: nightBakery.id },
  OpenRestaurant: { restaurantId: nightBakery.id },
  CloseRestaurant: { restaurantId: kitchenEight.id },
  MyRestaurants: { first: 10 },
  PlaceOrder: {
    input: {
      restaurantId: kitchenEight.id,
      items: [{ menuItemId: "menu-bao", quantity: 1 }],
      deliveryAddress: "ул. Лесная, 12",
      idempotencyKey: "contract-place",
    },
  },
};

const operations = Object.entries(documents).flatMap(([file, source]) =>
  parse(source)
    .definitions.filter(
      (definition): definition is OperationDefinitionNode =>
        definition.kind === Kind.OPERATION_DEFINITION,
    )
    .map((definition) => ({
      file,
      name: definition.name?.value ?? "",
      document: { kind: Kind.DOCUMENT, definitions: [definition] } as const,
    })),
);

function schemaType(name: string): GraphQLObjectType {
  return publicSchema.getType(name) as GraphQLObjectType;
}

describe("mock contract", () => {
  it("covers every customer operation of the schema", () => {
    const rootFields = [
      ...Object.keys(publicSchema.getQueryType()?.getFields() ?? {}),
      ...Object.keys(publicSchema.getMutationType()?.getFields() ?? {}),
    ];
    const store = createMockStore();
    expect(Object.keys(store.rootValue).sort()).toEqual(rootFields.sort());
  });

  it.each(operations)(
    "$name from $file conforms to the schema",
    async ({ name, document }) => {
      expect(validate(publicSchema, document)).toEqual([]);
      const variables = variablesByOperation[name];
      expect(variables, `variables for ${name}`).toBeDefined();

      const result = await execute({
        schema: publicSchema,
        document,
        rootValue: createMockStore().rootValue,
        variableValues: variables,
      });
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
