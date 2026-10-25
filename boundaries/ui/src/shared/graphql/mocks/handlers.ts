import {
  type ExecutionResult,
  execute,
  getOperationAST,
  parse,
  subscribe,
  validate,
} from "graphql";
import { HttpResponse } from "msw";
import { graphql } from "msw/graphql";

import { publicSchema } from "./schema";
import { createMockStore, type MockStore } from "./store";

const publicGraphql = graphql.link(/\/graphql$/);

const encoder = new TextEncoder();

// Subscriptions answer like Cosmo Router over SSE (ADR 36): one `next` event
// per result, `complete` at the end; graphql-sse reads this format. An
// aborted request (the client unsubscribed) only ends the stream: writing to
// or closing the response after the abort breaks the fetch of Node.
function eventStream(
  results: AsyncIterableIterator<ExecutionResult>,
  signal: AbortSignal,
) {
  signal.addEventListener("abort", () => void results.return?.());
  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      for await (const result of results) {
        if (signal.aborted) return;
        controller.enqueue(
          encoder.encode(`event: next\ndata: ${JSON.stringify(result)}\n\n`),
        );
      }
      if (signal.aborted) return;
      controller.enqueue(encoder.encode("event: complete\ndata:\n\n"));
      controller.close();
    },
    async cancel() {
      await results.return?.();
    },
  });
  return new Response(stream, {
    headers: { "content-type": "text/event-stream" },
  });
}

// Every operation is validated and executed against the public schema, so a
// mock cannot answer with a shape the real BFF would not produce.
export function createGraphqlHandlers(store: MockStore) {
  return [
    publicGraphql.operation(
      async ({ query, variables, operationName, request }) => {
        const document = parse(query);
        const errors = validate(publicSchema, document);
        if (errors.length > 0) return HttpResponse.json({ errors });

        const args = {
          schema: publicSchema,
          document,
          rootValue: store.rootValue,
          variableValues: variables,
        };
        if (
          getOperationAST(document, operationName)?.operation === "subscription"
        ) {
          const results = await subscribe(args);
          return Symbol.asyncIterator in results
            ? eventStream(results, request.signal)
            : HttpResponse.json(results);
        }
        return HttpResponse.json(await execute(args));
      },
    ),
  ];
}

export const mockStore = createMockStore();

export const graphqlHandlers = createGraphqlHandlers(mockStore);
