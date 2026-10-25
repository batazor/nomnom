import { cacheExchange, createClient, fetchExchange, mapExchange } from "urql";

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
  return createClient({
    url,
    exchanges: [
      cacheExchange,
      mapExchange({
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
    ],
    fetchOptions() {
      const headers: Record<string, string> = {};
      const traceparent = getTraceparent?.();
      if (traceparent) headers.traceparent = traceparent;
      const token = getAccessToken?.();
      if (token) headers.authorization = `Bearer ${token}`;
      return { headers };
    },
  });
}
