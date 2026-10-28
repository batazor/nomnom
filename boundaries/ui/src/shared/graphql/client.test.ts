import { afterEach, describe, expect, it, vi } from "vitest";

import { createGraphqlClient } from "./client";
import { BffInfoDocument } from "./generated/graphql";

describe("GraphQL client", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("uses only the validated public endpoint", async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
      Promise.resolve(
        new Response(JSON.stringify({ data: { bffInfo: { name: "BFF" } } }), {
          headers: { "content-type": "application/json" },
        }),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = createGraphqlClient({
      url: "https://public.example.test/graphql",
      persistedOperations: false,
    });
    await client.query("query Probe { bffInfo { name } }", {}).toPromise();
    const [requestUrl, requestInit] = fetchMock.mock.calls[0] ?? [];
    expect(requestUrl).toMatch(/^https:\/\/public\.example\.test\/graphql/);
    expect(requestInit).toEqual(expect.objectContaining({ method: "GET" }));
  });

  it("reports safe metadata without variables", async () => {
    const reportError = vi.fn();
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
      Promise.resolve(
        new Response(
          JSON.stringify({ errors: [{ message: "internal grpc detail" }] }),
          {
            status: 500,
            headers: {
              "content-type": "application/json",
              "x-request-id": "request-safe-123",
            },
          },
        ),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = createGraphqlClient({
      url: "https://public.example.test/graphql",
      getTraceparent: () =>
        "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
      reportError,
      persistedOperations: false,
    });

    await client
      .query("query Probe($secret: String!) { bffInfo { name } }", {
        secret: "do-not-report",
      })
      .toPromise();

    expect(reportError).toHaveBeenCalledWith({
      operationName: "query",
      requestId: "request-safe-123",
      kind: "business",
    });
    expect(JSON.stringify(reportError.mock.calls)).not.toContain(
      "do-not-report",
    );
    expect(fetchMock.mock.calls[0]?.[1]).toEqual(
      expect.objectContaining({
        headers: expect.objectContaining({
          traceparent:
            "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
          "graphql-client-name": "nomnom-ui",
        }),
      }),
    );
  });

  it("sends a persisted operation as its codegen hash without the text", async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
      Promise.resolve(
        new Response(JSON.stringify({ data: { bffInfo: { name: "BFF" } } }), {
          headers: { "content-type": "application/json" },
        }),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = createGraphqlClient({
      url: "https://public.example.test/graphql",
      persistedOperations: true,
    });

    await client.query(BffInfoDocument, {}).toPromise();

    const requestUrl = new URL(String(fetchMock.mock.calls[0]?.[0]));
    expect(requestUrl.searchParams.get("query")).toBeNull();
    expect(
      JSON.parse(requestUrl.searchParams.get("extensions") ?? "{}"),
    ).toEqual({
      persistedQuery: {
        version: 1,
        // The hex SHA-256 of the operation text, as in the BFF manifest.
        sha256Hash:
          "d3f387ead239a49cd2ab9e5708afd02fecf7c91fba9287371fd3d977763be52a",
      },
    });
  });
});
