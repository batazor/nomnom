import { persistedExchange } from "@urql/exchange-persisted";
import type { ExecutionResult } from "graphql";
import {
  createClient as createSseClient,
  type RequestParams,
} from "graphql-sse";
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

// Delivers the results of one subscription to sink and calls connected when
// its stream is open; returns the unsubscribe. Default: SSE to the Router.
// A persisted operation carries its hash in extensions and no query.
export type SubscriptionTransport = (
  request: Readonly<{
    query?: string;
    variables?: Record<string, unknown>;
    operationName?: string;
    extensions?: Record<string, unknown>;
  }>,
  sink: Readonly<{
    next: (result: ExecutionResult) => void;
    error: (error: unknown) => void;
    complete: () => void;
  }>,
  connected: () => void,
) => () => void;

type ClientOptions = Readonly<{
  url: string;
  // Mocks execute subscriptions in process instead of over SSE.
  subscriptions?: SubscriptionTransport;
  getTraceparent?: () => string | undefined;
  // Access token of the user BFF acts for; without it only public reads work.
  getAccessToken?: () => string | undefined;
  reportError?: (report: GraphqlErrorReport) => void;
  // BFF accepts only the operations of its manifest (ADR 3 of BFF): every
  // operation is sent as the SHA-256 hash codegen put in the document, never
  // as text. Off only for mocks, which execute the text.
  persistedOperations: boolean;
}>;

// Hash of the operation computed by codegen (persistedDocuments); a document
// without one was not generated and is not in the manifest of BFF.
function documentHash(_query: string, document: unknown): string {
  const hash = (document as { __meta__?: { hash?: unknown } }).__meta__?.hash;
  if (typeof hash !== "string") {
    throw new Error("GraphQL document has no persisted operation hash");
  }
  return hash;
}

export function createGraphqlClient({
  url,
  getTraceparent,
  getAccessToken,
  reportError,
  subscriptions,
  persistedOperations,
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

  // One SSE request per subscription (distinct connections mode) to the url
  // of the Market: the public gateway routes it like any other request, the
  // Router verifies Authorization, no WebSocket state.
  const transport =
    subscriptions ??
    ((): SubscriptionTransport => {
      const sse = createSseClient({
        url,
        headers,
        retryAttempts: Number.POSITIVE_INFINITY,
        on: { connected: refetchActiveQueries },
      });
      // graphql-sse types require a query; a persisted subscription sends
      // only its hash in extensions, which the Router resolves.
      return (request, sink) => sse.subscribe(request as RequestParams, sink);
    })();

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
      ...(persistedOperations
        ? [
            persistedExchange({
              enforcePersistedQueries: true,
              enableForMutation: true,
              enableForSubscriptions: true,
              generateHash: async (query, document) =>
                documentHash(query, document),
            }),
          ]
        : []),
      fetchExchange,
      subscriptionExchange({
        forwardSubscription(request) {
          return {
            subscribe: (sink) => ({
              unsubscribe: transport(request, sink, refetchActiveQueries),
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
