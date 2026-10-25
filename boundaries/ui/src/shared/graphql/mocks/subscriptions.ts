import { type ExecutionResult, parse, subscribe, validate } from "graphql";

import type { SubscriptionTransport } from "../client";
import { mockStore } from "./handlers";
import { publicSchema } from "./schema";

// Subscriptions of the mocks run in process against the same public schema
// and store as the MSW handlers: MSW cannot keep streamed responses open in
// Node fetch, and the Router side (Cosmo Streams over SSE, ADR 36) is not a
// thing the UI owns. The stream is "connected" once the iterator exists, so
// the client refetches its queries as it does after an SSE connect.
export const mockSubscriptions: SubscriptionTransport = (
  request,
  sink,
  connected,
) => {
  let results: AsyncIterableIterator<ExecutionResult> | undefined;
  let stopped = false;

  async function run() {
    const document = parse(request.query);
    const errors = validate(publicSchema, document);
    if (errors.length > 0) {
      sink.next({ errors });
      sink.complete();
      return;
    }
    const stream = await subscribe({
      schema: publicSchema,
      document,
      rootValue: mockStore.rootValue,
      variableValues: request.variables,
      operationName: request.operationName,
    });
    if (!(Symbol.asyncIterator in stream)) {
      sink.next(stream);
      sink.complete();
      return;
    }
    results = stream;
    if (stopped) {
      await stream.return?.();
      return;
    }
    connected();
    for await (const result of stream) sink.next(result);
    if (!stopped) sink.complete();
  }

  run().catch(sink.error);
  return () => {
    stopped = true;
    void results?.return?.();
  };
};
