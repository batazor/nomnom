import { afterEach, describe, expect, it, vi } from "vitest";

import { createGraphqlClient } from "./client";

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
        }),
      }),
    );
  });

  it("routes by the Market of the viewer", async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
      Promise.resolve(
        new Response(JSON.stringify({ data: { bffInfo: { name: "BFF" } } }), {
          headers: { "content-type": "application/json" },
        }),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);
    let market: string | undefined;
    const client = createGraphqlClient({
      url: "https://public.example.test/graphql",
      getMarket: () => market,
    });

    await client.query("query First { bffInfo { name } }", {}).toPromise();
    market = "berlin";
    await client.query("query Second { bffInfo { name } }", {}).toPromise();

    const headersOf = (call: number) =>
      fetchMock.mock.calls[call]?.[1]?.headers as Record<string, string>;
    expect(headersOf(0)).not.toHaveProperty("x-nomnom-market");
    expect(headersOf(1)).toEqual(
      expect.objectContaining({ "x-nomnom-market": "berlin" }),
    );
  });
});
