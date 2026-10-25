import { execute, getOperationAST, parse, validate } from "graphql";
import { HttpResponse } from "msw";
import { graphql } from "msw/graphql";

import { publicSchema } from "./schema";
import { bffError, createMockStore, type MockStore } from "./store";

const publicGraphql = graphql.link(/\/graphql$/);

// Every operation is validated and executed against the public schema, so a
// mock cannot answer with a shape the real BFF would not produce.
export function createGraphqlHandlers(store: MockStore) {
  return [
    publicGraphql.operation(async ({ query, variables, operationName }) => {
      const document = parse(query);
      const errors = validate(publicSchema, document);
      if (errors.length > 0) return HttpResponse.json({ errors });

      // Subscriptions run in process (mocks/subscriptions.ts), not over HTTP.
      if (
        getOperationAST(document, operationName)?.operation === "subscription"
      ) {
        return HttpResponse.json({
          errors: [{ message: "mock subscriptions run in process" }],
        });
      }
      const args = {
        schema: publicSchema,
        document,
        rootValue: store.rootValue,
        variableValues: variables,
      };
      return HttpResponse.json(await execute(args));
    }),
  ];
}

export const mockStore = createMockStore();

export const graphqlHandlers = createGraphqlHandlers(mockStore);

// A user who is not the operator: BFF answers simulator with PermissionDenied
// (AC-767), so the header shows no switch.
export const simulatorDeniedHandlers = [
  publicGraphql.query("DemoSimulator", () =>
    HttpResponse.json({
      data: null,
      errors: [
        bffError(
          "PermissionDenied",
          "simulator operation is not permitted",
        ).toJSON(),
      ],
    }),
  ),
];
