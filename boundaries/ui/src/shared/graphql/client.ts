import { createClient as createSseClient } from "graphql-sse";
import {
  type Client,
  cacheExchange,
  createClient,
  fetchExchange,
  makeOperation,
  mapExchange,
  type Operation,
  subscriptionExchange,
} from "urql";

export type GraphqlErrorReport = Readonly<{
  operationName: string;
  requestId?: string;
  kind: "business" | "transport";
}>;

type ClientOptions = Readonly<{
  url: string;
  getTraceparent?: () => string | undefined;
  // Access token of the user BFF acts for; without it only public reads work.
  getAccessToken?: () => string | undefined;
  reportError?: (report: GraphqlErrorReport) => void;
}>;

export function createGraphqlClient({
  url,
  getTraceparent,
  getAccessToken,
  reportError,
}: ClientOptions) {
  function headers(): Record<string, string> {
    const result: Record<string, string> = {};
    const traceparent = getTraceparent?.();
    if (traceparent) result.traceparent = traceparent;
    const token = getAccessToken?.();
    if (token) result.authorization = `Bearer ${token}`;
    return result;
  }

  // Cosmo Streams send only changes after subscribe (ADR 36): a change
  // between a query and its subscription, or during a reconnect, is otherwise
  // lost. Every (re)connected stream refetches the active queries.
  const activeQueries = new Map<number, Operation>();
  let client: Client | undefined;
  function refetchActiveQueries() {
    for (const operation of activeQueries.values()) {
      client?.reexecuteOperation(
        makeOperation(operation.kind, operation, {
          ...operation.context,
          requestPolicy: "network-only",
        }),
      );
    }
  }

  // One SSE request per subscription (distinct connections mode): the
  // Router verifies Authorization like any other request, no WebSocket state.
  const sse = createSseClient({
    url,
    headers,
    retryAttempts: Number.POSITIVE_INFINITY,
    on: { connected: refetchActiveQueries },
  });

  client = createClient({
    url,
    exchanges: [
      cacheExchange,
      mapExchange({
        onOperation(operation) {
          if (operation.kind === "query") {
            activeQueries.set(operation.key, operation);
          } else if (operation.kind === "teardown") {
            activeQueries.delete(operation.key);
          }
        },
        onError(error, operation) {
          reportError?.({
            operationName:
              operation.kind === "query" ? "query" : operation.kind,
            requestId: error.response?.headers.get("x-request-id") ?? undefined,
            kind: error.networkError ? "transport" : "business",
          });
        },
      }),
      fetchExchange,
      subscriptionExchange({
        forwardSubscription(request) {
          return {
            subscribe: (sink) => ({
              unsubscribe: sse.subscribe(
                { ...request, query: request.query ?? "" },
                sink,
              ),
            }),
          };
        },
      }),
    ],
    fetchOptions() {
      return { headers: headers() };
    },
  });
  return client;
}
